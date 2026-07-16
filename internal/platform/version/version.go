package version

import (
	"encoding/json"
	"net/http"
	"runtime"
)

type Info struct {
	Service string `json:"service"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	Go      string `json:"go"`
}

func New(service, version, commit, date string) Info {
	return Info{
		Service: service,
		Version: valueOrDefault(version, "dev"),
		Commit:  valueOrDefault(commit, "none"),
		Date:    valueOrDefault(date, "unknown"),
		Go:      runtime.Version(),
	}
}

func Handler(info Info) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(info)
	})
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
