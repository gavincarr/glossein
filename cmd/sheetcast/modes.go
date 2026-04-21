package main

import "time"

// gapModes is the inter-sentence silence table keyed by --mode name.
//
//	listen: passive comprehension — just enough time to close the sentence mentally
//	shadow: speak-along (repeat each sentence aloud after it plays)
//	drill:  prompt → silent recall → next prompt
var gapModes = map[string]time.Duration{
	"listen": 1200 * time.Millisecond,
	"shadow": 3500 * time.Millisecond,
	"drill":  6000 * time.Millisecond,
}

func modeNames() []string {
	return []string{"listen", "shadow", "drill"}
}
