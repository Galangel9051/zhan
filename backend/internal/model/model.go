package model

type Stack struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	ProjectName  string    `json:"project_name"`
	Directory    string    `json:"project_directory"`
	Files        []string  `json:"compose_files"`
	Desired      string    `json:"desired_state"`
	CreatedAt    string    `json:"created_at"`
	Repository   string    `json:"repository"`
	Branch       string    `json:"branch"`
	Polling      bool      `json:"polling_enabled"`
	Interval     int       `json:"poll_interval"`
	AutoDeploy   bool      `json:"auto_deploy"`
	Notify       bool      `json:"notify_on_failure"`
	NoCache      bool      `json:"no_cache"`
	BuildPull    bool      `json:"build_pull"`
	PullImages   bool      `json:"pull_images"`
	Remote       string    `json:"remote_commit"`
	Local        string    `json:"local_commit"`
	Attempted    string    `json:"last_attempted_commit"`
	Success      string    `json:"last_success_commit"`
	Failures     int       `json:"failure_count"`
	FailedCommit string    `json:"failed_commit"`
	RetryAt      int64     `json:"retry_at"`
	NextPoll     int64     `json:"next_poll_at"`
	Runtime      string    `json:"runtime_status"`
	Services     []Service `json:"services"`
	LastTask     *Task     `json:"last_task,omitempty"`
	RuntimeError string    `json:"runtime_error,omitempty"`
}
type Service struct {
	Name         string      `json:"name"`
	Image        string      `json:"image"`
	Build        bool        `json:"build"`
	Dependencies []string    `json:"dependencies"`
	Status       string      `json:"status"`
	Containers   []Container `json:"containers"`
}
type Container struct {
	Project      string `json:"-"`
	ID           string `json:"id"`
	Name         string `json:"name"`
	Service      string `json:"service"`
	State        string `json:"state"`
	Health       string `json:"health"`
	Image        string `json:"image"`
	Ports        string `json:"ports"`
	CreatedAt    string `json:"created_at"`
	StartedAt    string `json:"started_at"`
	RestartCount int    `json:"restart_count"`
}
type Discovery struct {
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Files  []string `json:"config_files"`
}
type Task struct {
	CancelRequested bool   `json:"cancel_requested"`
	ID              int64  `json:"id"`
	StackID         int64  `json:"stack_id"`
	StackName       string `json:"stack_name"`
	Type            string `json:"task_type"`
	Trigger         string `json:"trigger_type"`
	Service         string `json:"service"`
	Container       string `json:"container_id"`
	StartAfter      bool   `json:"start_after_deploy"`
	Commit          string `json:"commit_sha"`
	Status          string `json:"status"`
	Stage           string `json:"stage"`
	ExitCode        int    `json:"exit_code"`
	Error           string `json:"error_message"`
	CreatedAt       string `json:"created_at"`
	StartedAt       string `json:"started_at"`
	FinishedAt      string `json:"finished_at"`
}
type SMTP struct {
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	Username    string   `json:"username"`
	Password    string   `json:"password,omitempty"`
	PasswordSet bool     `json:"password_set"`
	TLS         string   `json:"tls_mode"`
	From        string   `json:"from"`
	Recipients  []string `json:"recipients"`
	Stages      []string `json:"stages"`
	Cooldown    int      `json:"cooldown_minutes"`
}
type Audit struct {
	ID        int64  `json:"id"`
	User      string `json:"user"`
	Action    string `json:"action"`
	Target    string `json:"target"`
	Result    string `json:"result"`
	CreatedAt string `json:"created_at"`
}
