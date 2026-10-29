package prompt

import (
	"os"
)

// StdinIsTerminal is a var so tests can force the TTY branch.
var StdinIsTerminal = func() bool { return WriterIsTerminal(os.Stdin) }
