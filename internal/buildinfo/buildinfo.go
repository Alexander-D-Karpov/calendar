package buildinfo

import (
	"runtime"
	"runtime/debug"
	"sync"
)

var (
	Version = ""
	Commit  = ""
	Date    = ""
)

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	Modified  bool   `json:"modified"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

var (
	once sync.Once
	info Info
)

func Get() Info {
	once.Do(func() {
		info = Info{
			Version:   Version,
			Commit:    Commit,
			Date:      Date,
			GoVersion: runtime.Version(),
			Platform:  runtime.GOOS + "/" + runtime.GOARCH,
		}
		if bi, ok := debug.ReadBuildInfo(); ok {
			if info.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
				info.Version = bi.Main.Version
			}
			for _, s := range bi.Settings {
				switch s.Key {
				case "vcs.revision":
					if info.Commit == "" {
						info.Commit = s.Value
					}
				case "vcs.time":
					if info.Date == "" {
						info.Date = s.Value
					}
				case "vcs.modified":
					info.Modified = s.Value == "true"
				}
			}
		}
		if info.Version == "" {
			info.Version = "dev"
		}
	})
	return info
}

func (i Info) ShortCommit() string {
	if len(i.Commit) > 12 {
		return i.Commit[:12]
	}
	return i.Commit
}

func (i Info) Release() string {
	if i.Version == "dev" && i.Commit != "" {
		return "calendar@dev-" + i.ShortCommit()
	}
	return "calendar@" + i.Version
}
