package service

import "context"

type Paths struct {
	ProjectRoot     string `json:"project_root"`
	BridgeBinary    string `json:"bridge_binary"`
	BridgeControl   string `json:"bridge_control"`
	EnvFile         string `json:"env_file"`
	StatePath       string `json:"state_path"`
	LogsDir         string `json:"logs_dir"`
	StdoutLog       string `json:"stdout_log"`
	StderrLog       string `json:"stderr_log"`
	LaunchAgentsDir string `json:"launch_agents_dir"`
	PlistPath       string `json:"plist_path"`
}

type Status struct {
	Label        string `json:"label"`
	Installed    bool   `json:"installed"`
	Loaded       bool   `json:"loaded"`
	Running      bool   `json:"running"`
	AutoStart    bool   `json:"auto_start"`
	PID          int    `json:"pid,omitempty"`
	UnmanagedPID int    `json:"unmanaged_pid,omitempty"`
	LastExit     *int   `json:"last_exit,omitempty"`
	Description  string `json:"description,omitempty"`
	Paths        Paths  `json:"paths"`
}

type Manager interface {
	Status(context.Context) (Status, error)
	SetAutoStart(context.Context, bool) error
	Start(context.Context) error
	Stop(context.Context) error
	Restart(context.Context) error
	Remove(context.Context) error
	StopUnmanaged(context.Context) error
	Paths() Paths
}

func New(projectRoot string) (Manager, error) {
	return newManager(projectRoot)
}
