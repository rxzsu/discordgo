//go:build goccy

package discordgo

import (
	goccy "github.com/goccy/go-json"
)

func init() {
	// Build with -tags goccy to use goccy/go-json.
	// Requires: go get github.com/goccy/go-json
	Marshal = goccy.Marshal
	Unmarshal = goccy.Unmarshal
}
