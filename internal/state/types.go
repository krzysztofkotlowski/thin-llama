package state

type State struct {
	Active    ActiveState               `json:"active"`
	Models    map[string]ModelState     `json:"models"`
	Processes map[string]ProcessState   `json:"processes"`
	Downloads map[string]DownloadStatus `json:"downloads"`
}

type ActiveState struct {
	Chat      string `json:"chat,omitempty"`
	Embedding string `json:"embedding,omitempty"`
}

type ModelState struct {
	Name             string `json:"name"`
	LocalPath        string `json:"local_path,omitempty"`
	Available        bool   `json:"available"`
	LastChecksumOK   bool   `json:"last_checksum_ok"`
	LastDownloadedAt string `json:"last_downloaded_at,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

type ProcessState struct {
	Role                string `json:"role"`
	ModelName           string `json:"model_name"`
	Port                int    `json:"port"`
	PID                 int    `json:"pid,omitempty"`
	Running             bool   `json:"running"`
	LastStartedAt       string `json:"last_started_at,omitempty"`
	LastExitAt          string `json:"last_exit_at,omitempty"`
	LastError           string `json:"last_error,omitempty"`
	StatusMessage       string `json:"status_message,omitempty"`
	OrphanDetected      bool   `json:"orphan_detected,omitempty"`
	RestartCount        int    `json:"restart_count,omitempty"`
	RestartSuppressed   bool   `json:"restart_suppressed,omitempty"`
	RestartSuppressedAt string `json:"restart_suppressed_at,omitempty"`
}

type DownloadStatus struct {
	ModelName string `json:"model_name"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updated_at,omitempty"`
	LastError string `json:"last_error,omitempty"`
}
