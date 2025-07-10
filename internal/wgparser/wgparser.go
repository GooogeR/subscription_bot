package wgparser

import (
	"bufio"
	"strings"
)

type Peer struct {
	PublicKey  string
	AllowedIPs string
}

func ParseWGConfig(config string) []Peer {
	var peers []Peer
	var current Peer
	inPeerBlock := false

	scanner := bufio.NewScanner(strings.NewReader(config))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "[Peer]" {
			if inPeerBlock {
				peers = append(peers, current)
				current = Peer{}
			}
			inPeerBlock = true
			continue
		}

		if inPeerBlock {
			if strings.HasPrefix(line, "PublicKey") {
				current.PublicKey = strings.TrimSpace(strings.SplitN(line, "=", 2)[1])
			} else if strings.HasPrefix(line, "AllowedIPs") {
				current.AllowedIPs = strings.TrimSpace(strings.SplitN(line, "=", 2)[1])
			}
		}
	}

	// Добавим последний
	if inPeerBlock {
		peers = append(peers, current)
	}

	return peers
}
