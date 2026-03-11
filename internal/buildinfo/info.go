package buildinfo

type Info struct {
	Version string
	Commit  string
	Date    string
}

func (i Info) RuntimeName() string {
	return "thin-llama"
}

func (i Info) GitRef() string {
	if i.Commit == "" {
		return "unknown"
	}
	return i.Commit
}

func (i Info) Capabilities() []string {
	return []string{
		"health",
		"ollama.tags",
		"ollama.chat",
		"ollama.embed",
		"ollama.pull",
		"models.catalog",
		"models.active",
		"metrics",
	}
}
