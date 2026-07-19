package main

import "time"

type jsonableStatus struct {
	Banner string		`json:"banner"`
	Status string		`json:"status"`
	Version string		`json:"version"`
	ID string			`json:"id"`
	UptimeSec int		`json:"uptime"`
	TotalHTTPReqs int	`json:"http-requests"`
	Disk struct {		
		Usage int64		`json:"usage"`
		Max int64		`json:"max"`
		Policy string	`json:"policy"`
	}					`json:"disk"`
	Cache struct {		
		Hits int 		`json:"hits"`
		Misses int		`json:"misses"`
	}					`json:"cache"`
}

type jsonableRepo struct {
	Name string			`json:"path"`
	Size int64			`json:"size"`
	Requests int64		`json:"requests"`
	Pinned bool			`json:"pinned"`
	SelfHosted bool		`json:"self-hosted"`
	Hidden bool			`json:"hidden"`
	LastError time.Time	`json:"last-error"`
}

type jsonableRepos struct {
	Status string					`json:"status"`
	Repos map[string]jsonableRepo	`json:"repos"`
	TotalSize int64					`json:"size"`
}

type jsonableOperation struct {
	Status string		`json:"status"`
	Transcript string	`json:"transcript"`
}

type jsonableRefresh struct {
	Status string		`json:"status"`
	Transcript string	`json:"transcript"`
	Name string			`json:"name"`
}

type jsonableRefreshAll struct {
	Repos map[string]jsonableRefresh	`json:"repos"`
	Fails int							`json:"fails"`
}

type jsonableEffectiveConfig struct {
	Status string		`json:"status"`
	Config string		`json:"config"`
}
