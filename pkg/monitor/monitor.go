package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jefflunt/kamal-watch/config"
	"github.com/jefflunt/kamal-watch/pkg/models"
	"github.com/jefflunt/kamal-watch/pkg/remote"
)

type Monitor struct {
	pool         *remote.SSHPool
	cfg          *config.Config
	inventories  sync.Map // map[string]*models.ServerInventory
	listeners    sync.Map // map[chan struct{}]struct{}
	prevCPUTicks sync.Map // map[string]map[int]cpuTicks
}

func NewMonitor(cfg *config.Config, pool *remote.SSHPool) *Monitor {
	return &Monitor{
		pool: pool,
		cfg:  cfg,
	}
}

func (m *Monitor) Start(ctx context.Context) {
	m.PollAll(ctx)

	ticker := time.NewTicker(m.cfg.PollInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.PollAll(ctx)
			}
		}
	}()
}

func (m *Monitor) PollAll(ctx context.Context) {
	var wg sync.WaitGroup
	for _, s := range m.cfg.Servers {
		wg.Add(1)
		go func(srv config.ServerConfig) {
			defer wg.Done()
			inv := m.PollServer(ctx, srv)
			m.inventories.Store(srv.Name, inv)
		}(s)
	}
	wg.Wait()
	m.notifyListeners()
}

func (m *Monitor) Subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	m.listeners.Store(ch, struct{}{})
	return ch
}

func (m *Monitor) Unsubscribe(ch chan struct{}) {
	m.listeners.Delete(ch)
	close(ch)
}

func (m *Monitor) notifyListeners() {
	m.listeners.Range(func(key, value any) bool {
		ch := key.(chan struct{})
		select {
		case ch <- struct{}{}:
		default:
		}
		return true
	})
}

func (m *Monitor) GetInventory(name string) (*models.ServerInventory, bool) {
	val, ok := m.inventories.Load(name)
	if !ok {
		return nil, false
	}
	return val.(*models.ServerInventory), true
}

func (m *Monitor) GetAllInventories() []*models.ServerInventory {
	var res []*models.ServerInventory
	for _, s := range m.cfg.Servers {
		if inv, ok := m.GetInventory(s.Name); ok {
			res = append(res, inv)
		} else {
			res = append(res, &models.ServerInventory{
				ServerName: s.Name,
				Host:       s.Host,
				Online:     false,
				Error:      "Not yet inventoried",
				Apps:       make(map[string]*models.KamalApp),
			})
		}
	}
	return res
}

type dockerContainerJSON struct {
	ID        string `json:"ID"`
	Names     string `json:"Names"`
	Image     string `json:"Image"`
	CreatedAt string `json:"CreatedAt"`
	Status    string `json:"Status"`
	State     string `json:"State"`
	Ports     string `json:"Ports"`
	Labels    string `json:"Labels"`
}

type dockerStatsJSON struct {
	ID       string `json:"ID"`
	Name     string `json:"Name"`
	CPUPerc  string `json:"CPUPerc"`
	MemPerc  string `json:"MemPerc"`
	MemUsage string `json:"MemUsage"`
	NetIO    string `json:"NetIO"`
	BlockIO  string `json:"BlockIO"`
	PIDs     string `json:"PIDs"`
}

func (m *Monitor) PollServer(parentCtx context.Context, srv config.ServerConfig) *models.ServerInventory {
	ctx, cancel := context.WithTimeout(parentCtx, 15*time.Second)
	defer cancel()

	inv := &models.ServerInventory{
		ServerName: srv.Name,
		Host:       srv.Host,
		LastPolled: time.Now(),
		Apps:       make(map[string]*models.KamalApp),
	}

	batchScript := `echo "___SECTION:SYS___" && uptime && nproc && echo "___SECTION:CPU___" && (grep -m1 -E '^(model name|Model)\s*:' /proc/cpuinfo 2>/dev/null || true) && echo "SOCKETS:$(grep 'physical id' /proc/cpuinfo 2>/dev/null | sort -u | wc -l)" && echo "___SECTION:PROC_STAT___" && (grep '^cpu[0-9]' /proc/stat 2>/dev/null || true) && echo "___SECTION:TOP___" && (top -bn1 | head -n 5 2>/dev/null || true) && echo "___SECTION:DISK___" && df -h / && echo "___SECTION:MEM___" && free -h && echo "___SECTION:HOST_UPDATES___" && (if [ -f /run/reboot-required ] || [ -f /var/run/reboot-required ]; then echo "REBOOT_REQUIRED:1"; elif command -v needs-restarting >/dev/null 2>&1; then if needs-restarting -r >/dev/null 2>&1; then echo "REBOOT_REQUIRED:0"; else echo "REBOOT_REQUIRED:1"; fi; else echo "REBOOT_REQUIRED:0"; fi; echo "REBOOT_PKGS_START"; cat /run/reboot-required.pkgs /var/run/reboot-required.pkgs 2>/dev/null | sort -u || true; echo "REBOOT_PKGS_END"; echo "UPDATES_START"; if [ -f /var/lib/update-notifier/updates-available ]; then cat /var/lib/update-notifier/updates-available 2>/dev/null || true; elif [ -f /var/run/motd.dynamic ]; then cat /var/run/motd.dynamic 2>/dev/null || true; fi; echo "UPDATES_END") && echo "___SECTION:STATS___" && (docker stats --no-stream --format '{{json .}}' 2>/dev/null || true) && echo "___SECTION:DOCKER___" && docker ps -a --format '{{json .}}' && echo "___SECTION:PROXY___" && (docker exec kamal-proxy kamal-proxy list 2>/dev/null || true) && echo "___SECTION:AUDIT___" && head -n 30 "$HOME"/.kamal/*-audit.log 2>/dev/null || true`

	out, err := m.pool.RunCommand(ctx, srv.Name, batchScript)
	if err != nil {
		inv.Online = false
		inv.Error = err.Error()
		var backoffErr *remote.BackoffError
		if errors.As(err, &backoffErr) && !backoffErr.LastRetry.IsZero() {
			inv.LastPolled = backoffErr.LastRetry
		}
		return inv
	}

	inv.Online = true
	inv.Health.DockerActive = true

	sections := strings.Split(string(out), "___SECTION:")
	sectionMap := make(map[string]string)
	for _, sec := range sections {
		if strings.TrimSpace(sec) == "" {
			continue
		}
		parts := strings.SplitN(sec, "___", 2)
		if len(parts) == 2 {
			title := strings.TrimSpace(parts[0])
			body := strings.TrimPrefix(parts[1], "\n")
			sectionMap[title] = body
		}
	}

	// Parse System Health
	if sys, ok := sectionMap["SYS"]; ok {
		lines := strings.Split(strings.TrimSpace(sys), "\n")
		if len(lines) >= 1 {
			inv.Health.Uptime = lines[0]
			if idx := strings.Index(lines[0], "load average:"); idx != -1 {
				inv.Health.LoadAvg = strings.TrimSpace(lines[0][idx+len("load average:"):])
			}
		}
		if len(lines) >= 2 {
			if cores, err := strconv.Atoi(strings.TrimSpace(lines[1])); err == nil {
				inv.Health.CPUCores = cores
			}
		}
	}

	// Parse CPU info (Model, Sockets)
	if cpuOut, ok := sectionMap["CPU"]; ok {
		for _, line := range strings.Split(cpuOut, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "SOCKETS:") {
				val := strings.TrimSpace(strings.TrimPrefix(line, "SOCKETS:"))
				if s, err := strconv.Atoi(val); err == nil && s > 0 {
					inv.Health.CPUSockets = s
				}
			} else if strings.HasPrefix(line, "model name") || strings.HasPrefix(line, "Model") {
				inv.Health.CPUModel = CleanCPUModel(line)
			}
		}
	}
	if inv.Health.CPUSockets <= 0 {
		inv.Health.CPUSockets = 1
	}

	// Parse Per-Core CPU Usage
	if statOut, ok := sectionMap["PROC_STAT"]; ok {
		inv.Health.CPUCoresUsage = m.parseProcStat(srv.Name, statOut)
		if inv.Health.CPUCores <= 0 && len(inv.Health.CPUCoresUsage) > 0 {
			inv.Health.CPUCores = len(inv.Health.CPUCoresUsage)
		}
	}

	// Parse Top info (CPU usage %, tasks)
	if topOut, ok := sectionMap["TOP"]; ok {
		lines := strings.Split(strings.TrimSpace(topOut), "\n")
		for _, l := range lines {
			if strings.Contains(l, "Tasks:") {
				inv.Health.TasksSummary = strings.TrimSpace(l)
			}
			if strings.Contains(l, "%Cpu(s):") || strings.Contains(l, "CPU:") {
				// e.g. %Cpu(s):  0.0 us,  0.3 sy,  0.0 ni, 99.7 id
				if idx := strings.Index(l, "id"); idx != -1 {
					prefix := strings.TrimSpace(l[:idx])
					parts := strings.Fields(prefix)
					if len(parts) > 0 {
						idleStr := parts[len(parts)-1]
						if idle, err := strconv.ParseFloat(idleStr, 64); err == nil {
							inv.Health.CPUUsagePerc = 100.0 - idle
						}
					}
				}
			}
		}
	}

	// Parse Disk
	if disk, ok := sectionMap["DISK"]; ok {
		lines := strings.Split(strings.TrimSpace(disk), "\n")
		if len(lines) >= 2 {
			fields := strings.Fields(lines[1])
			if len(fields) >= 5 {
				inv.Health.DiskUsage = fmt.Sprintf("%s/%s (%s)", fields[2], fields[1], fields[4])
			}
		}
	}

	// Parse Memory & Swap
	if mem, ok := sectionMap["MEM"]; ok {
		lines := strings.Split(strings.TrimSpace(mem), "\n")
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) >= 7 && fields[0] == "Mem:" {
				inv.Health.MemTotal = fields[1]
				inv.Health.MemUsed = fields[2]
				inv.Health.MemAvailable = fields[6]
			} else if len(fields) >= 3 && fields[0] == "Swap:" {
				inv.Health.SwapTotal = fields[1]
				inv.Health.SwapUsed = fields[2]
			}
		}
	}

	// Parse Host Updates & Reboot-Required State
	if hostUpdates, ok := sectionMap["HOST_UPDATES"]; ok {
		parseHostUpdates(hostUpdates, &inv.Health)
	}

	// Parse Container Stats
	statsMap := make(map[string]models.ContainerStats)
	if statsOut, ok := sectionMap["STATS"]; ok {
		for _, line := range strings.Split(statsOut, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var sj dockerStatsJSON
			if err := json.Unmarshal([]byte(line), &sj); err != nil {
				continue
			}
			cpuPerc, _ := strconv.ParseFloat(strings.TrimSuffix(sj.CPUPerc, "%"), 64)
			memPerc, _ := strconv.ParseFloat(strings.TrimSuffix(sj.MemPerc, "%"), 64)

			memParts := strings.Split(sj.MemUsage, "/")
			memUse := sj.MemUsage
			memLimit := ""
			if len(memParts) == 2 {
				memUse = strings.TrimSpace(memParts[0])
				memLimit = strings.TrimSpace(memParts[1])
			}
			memBytes := parseBytes(memUse)

			cs := models.ContainerStats{
				CPUPerc:  cpuPerc,
				MemUsage: memUse,
				MemLimit: memLimit,
				MemBytes: memBytes,
				MemPerc:  memPerc,
				NetIO:    sj.NetIO,
				BlockIO:  sj.BlockIO,
				PIDs:     sj.PIDs,
			}
			statsMap[sj.Name] = cs
			statsMap[sj.ID] = cs
		}
	}

	// Parse Proxy Routes
	proxyRoutes := make(map[string]*models.ProxyRoute)
	if proxyOut, ok := sectionMap["PROXY"]; ok {
		lines := strings.Split(proxyOut, "\n")
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) >= 6 && fields[0] != "Service" {
				p := &models.ProxyRoute{
					Service: fields[0],
					Host:    fields[1],
					Path:    fields[2],
					Target:  fields[3],
					State:   fields[4],
					TLS:     fields[5],
				}
				proxyRoutes[p.Service] = p
			}
		}
	}

	// Parse Audit Logs
	auditLogs := make(map[string][]models.DeployEvent)
	if auditOut, ok := sectionMap["AUDIT"]; ok {
		fileSections := strings.Split(auditOut, "==>")
		for _, fSec := range fileSections {
			fSec = strings.TrimSpace(fSec)
			if fSec == "" {
				continue
			}
			parts := strings.SplitN(fSec, "<==", 2)
			if len(parts) == 2 {
				filePath := strings.TrimSpace(parts[0])
				body := parts[1]
				idx := strings.LastIndex(filePath, "/")
				fileName := filePath
				if idx != -1 {
					fileName = filePath[idx+1:]
				}
				appName := strings.TrimSuffix(fileName, "-audit.log")
				auditLogs[appName] = parseAuditLines(body)
			}
		}
	}

	// Parse Docker Containers
	if dockerOut, ok := sectionMap["DOCKER"]; ok {
		lines := strings.Split(dockerOut, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var dj dockerContainerJSON
			if err := json.Unmarshal([]byte(line), &dj); err != nil {
				continue
			}

			if dj.Names == "kamal-proxy" {
				inv.ProxyRunning = strings.HasPrefix(strings.ToLower(dj.State), "running")
			}
			if dj.Names == "registry" {
				inv.RegistryRunning = strings.HasPrefix(strings.ToLower(dj.State), "running")
			}

			inst := parseContainerInstance(dj)
			if st, ok := statsMap[inst.Name]; ok {
				inst.Stats = st
			} else if st, ok := statsMap[inst.ID]; ok {
				inst.Stats = st
			}

			inv.AllContainers = append(inv.AllContainers, inst)

			// Determine logical service name:
			// Normalizes accessories (e.g. myapp-db -> myapp, billing-db -> billing)
			serviceKey := inst.Service
			if serviceKey == "" {
				if strings.Contains(inst.Name, "-") {
					serviceKey = strings.Split(inst.Name, "-")[0]
				} else {
					serviceKey = inst.Name
				}
			} else if strings.HasSuffix(serviceKey, "-db") {
				serviceKey = strings.TrimSuffix(serviceKey, "-db")
			}

			app, exists := inv.Apps[serviceKey]
			if !exists {
				app = &models.KamalApp{
					Name: serviceKey,
				}
				if route, ok := proxyRoutes[serviceKey+"-web"]; ok {
					app.ProxyRoute = route
				}
				if history, ok := auditLogs[serviceKey]; ok {
					app.DeployHistory = history
					if len(history) > 0 {
						last := history[len(history)-1]
						app.LastDeployedAt = &last.Timestamp
						app.LastDeployedBy = last.User
						if last.Version != "" {
							app.ActiveVersion = last.Version
						}
					}
				}
				inv.Apps[serviceKey] = app
			}

			// Group container role
			isAccessory := strings.Contains(inst.Name, "-db") || inst.Role == "accessory" || (inst.Role != "web" && inst.Role != "job" && inst.Role != "")
			if isAccessory && inst.Role != "web" && inst.Role != "job" {
				app.Accessories = append(app.Accessories, inst)
			} else if inst.Role == "web" {
				if inst.State == models.StateRunning {
					app.WebInstances = append(app.WebInstances, inst)
					if app.ActiveVersion == "" {
						app.ActiveVersion = inst.Version
					}
				} else {
					app.OldInstances = append(app.OldInstances, inst)
				}
			} else if inst.Role == "job" {
				if inst.State == models.StateRunning {
					app.JobInstances = append(app.JobInstances, inst)
				} else {
					app.OldInstances = append(app.OldInstances, inst)
				}
			} else {
				if inst.State == models.StateRunning {
					app.Accessories = append(app.Accessories, inst)
				} else {
					app.OldInstances = append(app.OldInstances, inst)
				}
			}
		}
	}

	// Calculate Aggregate stats per application
	for _, app := range inv.Apps {
		var totalCPU, totalMemPerc float64
		var totalMemBytes int64
		var totalCont, runningCont int

		addInst := func(inst models.ServiceRoleInstance) {
			totalCont++
			if inst.State == models.StateRunning {
				runningCont++
				totalCPU += inst.Stats.CPUPerc
				totalMemPerc += inst.Stats.MemPerc
				totalMemBytes += inst.Stats.MemBytes
			}
		}

		for _, inst := range app.WebInstances {
			addInst(inst)
		}
		for _, inst := range app.JobInstances {
			addInst(inst)
		}
		for _, inst := range app.Accessories {
			addInst(inst)
		}
		for _, _ = range app.OldInstances {
			totalCont++
		}

		totalMemUsage := "-"
		if totalMemBytes > 0 {
			totalMemUsage = formatBytes(totalMemBytes)
		}

		app.Aggregates = models.ServiceAggregates{
			TotalCPU:          totalCPU,
			TotalMemPerc:      totalMemPerc,
			TotalMemBytes:     totalMemBytes,
			TotalMemUsage:     totalMemUsage,
			TotalContainers:   totalCont,
			RunningContainers: runningCont,
		}
	}

	return inv
}

var (
	auditLineRegex       = regexp.MustCompile(`^\[(.*?)\]\s+\[(.*?)\]\s+(.*)$`)
	updatesTotalRegex    = regexp.MustCompile(`(?i)(\d+)\s+(?:updates?|packages?)\s+(?:can\s+be\s+applied|can\s+be\s+updated|are\s+available)`)
	updatesSecurityRegex = regexp.MustCompile(`(?i)(\d+)\s+(?:of\s+these\s+updates?\s+(?:is\s+a|are)\s+standard\s+security\s+updates?|updates?\s+(?:is\s+a|are)\s+(?:standard\s+)?security\s+updates?|are\s+security\s+updates?|security\s+updates?)`)
)

func parseHostUpdates(body string, health *models.HostHealth) {
	if health == nil {
		return
	}

	var mode string
	var summaryLines []string
	seenPkgs := make(map[string]bool)

	for _, rawLine := range strings.Split(body, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		switch {
		case strings.HasPrefix(line, "REBOOT_REQUIRED:"):
			val := strings.TrimSpace(strings.TrimPrefix(line, "REBOOT_REQUIRED:"))
			health.RebootRequired = val == "1" || strings.EqualFold(val, "true")
		case line == "REBOOT_PKGS_START":
			mode = "pkgs"
		case line == "REBOOT_PKGS_END":
			mode = ""
		case line == "UPDATES_START":
			mode = "updates"
		case line == "UPDATES_END":
			mode = ""
		case mode == "pkgs":
			if !seenPkgs[line] {
				seenPkgs[line] = true
				health.RebootRequiredPkgs = append(health.RebootRequiredPkgs, line)
			}
		case mode == "updates":
			lower := strings.ToLower(line)
			if strings.HasPrefix(lower, "to see these additional updates") ||
				strings.HasPrefix(lower, "expanded security maintenance") ||
				strings.HasPrefix(lower, "enable esm") ||
				strings.HasPrefix(lower, "see https://") {
				continue
			}

			matched := false
			if m := updatesTotalRegex.FindStringSubmatch(line); len(m) == 2 {
				if n, err := strconv.Atoi(m[1]); err == nil {
					health.UpdatesAvailable = n
					matched = true
				}
			}
			if m := updatesSecurityRegex.FindStringSubmatch(line); len(m) == 2 {
				if n, err := strconv.Atoi(m[1]); err == nil {
					health.SecurityUpdates = n
					matched = true
				}
			}
			if matched {
				summaryLines = append(summaryLines, line)
			}
		}
	}

	if len(summaryLines) > 0 {
		health.UpdatesSummary = strings.Join(summaryLines, " ")
	}
}

func parseAuditLines(body string) []models.DeployEvent {
	var events []models.DeployEvent
	lines := strings.Split(body, "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "==>") {
			continue
		}
		matches := auditLineRegex.FindStringSubmatch(l)
		if len(matches) == 4 {
			t, err := time.Parse(time.RFC3339, matches[1])
			if err != nil {
				t, _ = time.Parse("2006-01-02T15:04:05Z", matches[1])
			}
			user := matches[2]
			action := matches[3]
			version := ""

			if idx := strings.Index(action, "version "); idx != -1 {
				v := strings.TrimSpace(action[idx+len("version "):])
				if f := strings.Fields(v); len(f) > 0 {
					version = f[0]
				}
			}

			events = append(events, models.DeployEvent{
				Timestamp: t,
				User:      user,
				Action:    action,
				Version:   version,
				Raw:       l,
			})
		}
	}
	return events
}

func parseContainerInstance(dj dockerContainerJSON) models.ServiceRoleInstance {
	inst := models.ServiceRoleInstance{
		ID:         dj.ID,
		Name:       dj.Names,
		Image:      dj.Image,
		Status:     dj.Status,
		Ports:      dj.Ports,
		CreatedRaw: dj.CreatedAt,
	}

	if strings.HasPrefix(strings.ToLower(dj.State), "running") {
		inst.State = models.StateRunning
	} else {
		inst.State = models.StateExited
	}

	labels := make(map[string]string)
	for _, pair := range strings.Split(dj.Labels, ",") {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			labels[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}

	if s, ok := labels["service"]; ok {
		inst.Service = s
	}
	if r, ok := labels["role"]; ok {
		inst.Role = r
	}

	if inst.Service == "" {
		parts := strings.Split(dj.Names, "-")
		if len(parts) >= 3 {
			inst.Service = parts[0]
			inst.Role = parts[1]
			inst.Version = parts[2]
		}
	}

	if idx := strings.LastIndex(dj.Image, ":"); idx != -1 {
		inst.Version = dj.Image[idx+1:]
	}

	if inst.Service == "" && strings.Contains(dj.Names, "-") {
		parts := strings.SplitN(dj.Names, "-", 2)
		inst.Service = parts[0]
		inst.Role = "accessory"
	}

	return inst
}

func parseBytes(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0
	}
	var numStr string
	var unit string
	for i, r := range s {
		if (r >= '0' && r <= '9') || r == '.' {
			numStr += string(r)
		} else {
			unit = strings.TrimSpace(s[i:])
			break
		}
	}
	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0
	}
	unit = strings.ToLower(unit)
	switch {
	case strings.HasPrefix(unit, "t"):
		return int64(val * 1024 * 1024 * 1024 * 1024)
	case strings.HasPrefix(unit, "g"):
		return int64(val * 1024 * 1024 * 1024)
	case strings.HasPrefix(unit, "m"):
		return int64(val * 1024 * 1024)
	case strings.HasPrefix(unit, "k"):
		return int64(val * 1024)
	case strings.HasPrefix(unit, "b"):
		return int64(val)
	default:
		return int64(val)
	}
}

func formatBytes(b int64) string {
	if b <= 0 {
		return "-"
	}
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
		tib = 1024 * gib
	)
	switch {
	case b >= tib:
		return fmt.Sprintf("%.2fTiB", float64(b)/float64(tib))
	case b >= gib:
		return fmt.Sprintf("%.2fGiB", float64(b)/float64(gib))
	case b >= mib:
		return fmt.Sprintf("%.1fMiB", float64(b)/float64(mib))
	case b >= kib:
		return fmt.Sprintf("%.1fKiB", float64(b)/float64(kib))
	default:
		return fmt.Sprintf("%dB", b)
	}
}

type cpuTicks struct {
	idle  uint64
	total uint64
}

var (
	cpuFreqRegex      = regexp.MustCompile(`(?i)\s*@\s*[0-9.]+\s*[mgt]hz\b`)
	cpuParenRegex     = regexp.MustCompile(`\([rR]\)|\([tT][mM]\)`)
	cpuCoreCountRegex = regexp.MustCompile(`(?i)\b\d+-Core Processor\b|\b\d+-Core\b`)
	cpuProcRegex      = regexp.MustCompile(`(?i)\b(Processor|CPU)\b`)
	cpuRyzenTierRegex = regexp.MustCompile(`(?i)\bRyzen\s+[3579]\b`)
	cpuIntelCoreRegex = regexp.MustCompile(`(?i)\bCore\s+(i[3579]|vPro)\b`)
	cpuMultipleSpaces = regexp.MustCompile(`\s+`)
)

func CleanCPUModel(raw string) string {
	s := strings.TrimSpace(raw)
	if idx := strings.Index(s, ":"); idx != -1 {
		s = strings.TrimSpace(s[idx+1:])
	}
	s = cpuFreqRegex.ReplaceAllString(s, "")
	s = cpuParenRegex.ReplaceAllString(s, "")
	s = cpuCoreCountRegex.ReplaceAllString(s, "")
	s = cpuProcRegex.ReplaceAllString(s, "")
	s = cpuRyzenTierRegex.ReplaceAllString(s, "")
	s = cpuIntelCoreRegex.ReplaceAllString(s, "$1")
	s = regexp.MustCompile(`(?i)\b(Ryzen|Core)\b`).ReplaceAllString(s, "")
	s = cpuMultipleSpaces.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	if s == "" {
		return "CPU"
	}
	return s
}

func (m *Monitor) parseProcStat(serverName, statOut string) []float64 {
	lines := strings.Split(statOut, "\n")
	type coreStat struct {
		index int
		ticks cpuTicks
	}
	var samples []coreStat

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "cpu") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] == "cpu" {
			// Skip aggregate "cpu" line
			continue
		}
		coreIdxStr := strings.TrimPrefix(fields[0], "cpu")
		coreIdx, err := strconv.Atoi(coreIdxStr)
		if err != nil {
			continue
		}

		var nums []uint64
		for _, f := range fields[1:] {
			v, _ := strconv.ParseUint(f, 10, 64)
			nums = append(nums, v)
		}
		if len(nums) < 4 {
			continue
		}
		user := nums[0]
		nice := nums[1]
		system := nums[2]
		idle := nums[3]
		var iowait, irq, softirq, steal uint64
		if len(nums) > 4 {
			iowait = nums[4]
		}
		if len(nums) > 5 {
			irq = nums[5]
		}
		if len(nums) > 6 {
			softirq = nums[6]
		}
		if len(nums) > 7 {
			steal = nums[7]
		}

		idleTicks := idle + iowait
		nonIdle := user + nice + system + irq + softirq + steal
		totalTicks := idleTicks + nonIdle

		samples = append(samples, coreStat{
			index: coreIdx,
			ticks: cpuTicks{idle: idleTicks, total: totalTicks},
		})
	}

	if len(samples) == 0 {
		return nil
	}

	maxIdx := -1
	for _, s := range samples {
		if s.index > maxIdx {
			maxIdx = s.index
		}
	}
	usages := make([]float64, maxIdx+1)

	var prevMap map[int]cpuTicks
	if val, ok := m.prevCPUTicks.Load(serverName); ok {
		prevMap = val.(map[int]cpuTicks)
	}
	nextMap := make(map[int]cpuTicks)

	for _, s := range samples {
		nextMap[s.index] = s.ticks
		if prev, ok := prevMap[s.index]; ok && s.ticks.total > prev.total {
			deltaTotal := s.ticks.total - prev.total
			deltaIdle := s.ticks.idle - prev.idle
			if deltaTotal > 0 && deltaTotal >= deltaIdle {
				usage := float64(deltaTotal-deltaIdle) / float64(deltaTotal) * 100.0
				if usage < 0 {
					usage = 0
				} else if usage > 100 {
					usage = 100
				}
				usages[s.index] = usage
			}
		} else if s.ticks.total > 0 {
			busy := s.ticks.total - s.ticks.idle
			usage := float64(busy) / float64(s.ticks.total) * 100.0
			if usage < 0 {
				usage = 0
			} else if usage > 100 {
				usage = 100
			}
			usages[s.index] = usage
		}
	}

	m.prevCPUTicks.Store(serverName, nextMap)
	return usages
}
