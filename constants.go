package main


const loreboxVersion = "v0.4"
const infoRefs = "/info/refs"
const defaultConfigFile = "~/.config/lorebox/client.yml"
const (
	authLevelNone int = iota
	authLevelFetch
	authLevelPush
	authLevelAdmin
)

