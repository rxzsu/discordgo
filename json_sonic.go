//go:build sonic

package discordgo

import "github.com/bytedance/sonic"

func init() {
	// Build with -tags sonic to use Sonic (amd64-optimized JSON).
	// Requires: go get github.com/bytedance/sonic
	Marshal = sonic.Marshal
	Unmarshal = sonic.Unmarshal
}
