module github.com/sophium/erun

go 1.25.5

require (
	github.com/adrg/xdg v0.5.3
	github.com/briandowns/spinner v1.23.2
	github.com/manifoldco/promptui v0.9.0
	github.com/sophium/erun/erun-common v0.0.0
	github.com/spf13/cobra v1.10.2
	golang.org/x/term v0.33.0
)

require (
	github.com/chzyer/readline v0.0.0-20180603132655-2972be24d48e // indirect
	github.com/fatih/color v1.7.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mattn/go-colorable v0.1.2 // indirect
	github.com/mattn/go-isatty v0.0.8 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	golang.org/x/sys v0.40.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/sophium/erun/erun-common => ../erun-common
