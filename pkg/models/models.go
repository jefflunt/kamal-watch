package models

import "time"

type ContainerState string

const (
	StateRunning ContainerState = "running"
	StateExited  ContainerState = "exited"
	StateUnknown ContainerState = "unknown"
)

type ContainerStats struct {
	CPUPerc     float64 `json:"cpu_perc"`
	MemUsage    string  `json:"mem_usage"`
	MemLimit    string  `json:"mem_limit"`
	MemBytes    int64   `json:"mem_bytes"`
	MemPerc     float64 `json:"mem_perc"`
	NetIO       string  `json:"net_io"`
	BlockIO     string  `json:"block_io"`
	PIDs        string  `json:"pids"`
}

type ServiceRoleInstance struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Service     string         `json:"service"`
	Role        string         `json:"role"`
	Version     string         `json:"version"`
	Image       string         `json:"image"`
	State       ContainerState `json:"state"`
	Status      string         `json:"status"`
	Created     time.Time      `json:"created"`
	CreatedRaw  string         `json:"created_raw"`
	Ports       string         `json:"ports"`
	Health      string         `json:"health"`
	IsLatest    bool           `json:"is_latest"`
	Stats       ContainerStats `json:"stats"`
}

type ProxyRoute struct {
	Service string `json:"service"`
	Host    string `json:"host"`
	Path    string `json:"path"`
	Target  string `json:"target"`
	State   string `json:"state"`
	TLS     string `json:"tls"`
}

type DeployEvent struct {
	Timestamp time.Time `json:"timestamp"`
	User      string    `json:"user"`
	Action    string    `json:"action"`
	Version   string    `json:"version"`
	Raw       string    `json:"raw"`
}

type ServiceAggregates struct {
	TotalCPU          float64 `json:"total_cpu"`
	TotalMemPerc      float64 `json:"total_mem_perc"`
	TotalMemBytes     int64   `json:"total_mem_bytes"`
	TotalMemUsage     string  `json:"total_mem_usage"`
	TotalContainers   int     `json:"total_containers"`
	RunningContainers int     `json:"running_containers"`
}

type KamalApp struct {
	Name           string                `json:"name"`
	ActiveVersion  string                `json:"active_version"`
	LastDeployedAt *time.Time            `json:"last_deployed_at"`
	LastDeployedBy string                `json:"last_deployed_by"`
	WebInstances   []ServiceRoleInstance `json:"web_instances"`
	JobInstances   []ServiceRoleInstance `json:"job_instances"`
	Accessories    []ServiceRoleInstance `json:"accessories"`
	OldInstances   []ServiceRoleInstance `json:"old_instances"`
	ProxyRoute     *ProxyRoute           `json:"proxy_route"`
	DeployHistory  []DeployEvent         `json:"deploy_history"`
	Aggregates     ServiceAggregates     `json:"aggregates"`
}

type HostHealth struct {
	Uptime             string   `json:"uptime"`
	LoadAvg            string   `json:"load_avg"`
	CPUUsagePerc       float64  `json:"cpu_usage_perc"`
	CPUCores           int      `json:"cpu_cores"`
	DiskUsage          string   `json:"disk_usage"`
	MemTotal           string   `json:"mem_total"`
	MemUsed            string   `json:"mem_used"`
	MemAvailable       string   `json:"mem_available"`
	MemUsedPerc        float64  `json:"mem_used_perc"`
	SwapTotal          string   `json:"swap_total"`
	SwapUsed           string   `json:"swap_used"`
	SwapUsedPerc       float64  `json:"swap_used_perc"`
	TasksSummary       string   `json:"tasks_summary"`
	DockerActive       bool     `json:"docker_active"`
	RebootRequired     bool     `json:"reboot_required"`
	RebootRequiredPkgs []string `json:"reboot_required_pkgs,omitempty"`
	UpdatesAvailable   int      `json:"updates_available"`
	SecurityUpdates    int      `json:"security_updates"`
	UpdatesSummary     string   `json:"updates_summary,omitempty"`
}

type ServerInventory struct {
	ServerName      string                `json:"server_name"`
	Host            string                `json:"host"`
	LastPolled      time.Time             `json:"last_polled"`
	Online          bool                  `json:"online"`
	Error           string                `json:"error,omitempty"`
	Health          HostHealth            `json:"health"`
	Apps            map[string]*KamalApp  `json:"apps"`
	ProxyRunning    bool                  `json:"proxy_running"`
	RegistryRunning bool                  `json:"registry_running"`
	AllContainers   []ServiceRoleInstance `json:"all_containers"`
}
