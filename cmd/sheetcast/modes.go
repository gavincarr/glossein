package main

import "time"

// gapModes is the inter-sentence silence table keyed by --mode name.
//
//	listen: passive comprehension — just enough time to close the sentence mentally
//	shadow: speak-along (repeat each sentence aloud after it plays)
//	repeat: same gap as listen (placeholder for future tuning)
var gapModes = map[string]time.Duration{
	"listen": 1000 * time.Millisecond,
	"shadow": 3500 * time.Millisecond,
	"repeat": 1000 * time.Millisecond,
}

func modeNames() []string {
	return []string{"listen", "shadow", "repeat"}
}
