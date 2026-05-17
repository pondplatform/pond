package api

import "time"

type CommandLog struct {
	Line     string    `json:"line"`
	LoggedAt time.Time `json:"loggedAt"`
}
