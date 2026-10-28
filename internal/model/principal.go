package model

type Principal struct {
	Subject       string   `mapstructure:"subject" json:"subject"`
	Name          string   `mapstructure:"name" json:"name"`
	Admin         bool     `mapstructure:"admin" json:"admin"`
	Disabled      bool     `mapstructure:"disabled" json:"disabled"`
	Projects      []string `mapstructure:"projects" json:"projects"`
	SyncProjects  []string `mapstructure:"sync_projects" json:"syncProjects"`
	SyncSources   []string `mapstructure:"sync_sources" json:"syncSources"`
	UploadSources []string `mapstructure:"upload_sources" json:"uploadSources"`
}
