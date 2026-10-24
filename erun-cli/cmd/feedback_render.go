package cmd

import (
	"strings"

	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	timeFlagUsage    = "Print the elapsed runtime after the command finishes."
	VerboseFlagUsage = "Increase verbosity: -v streams external tool output, -vv adds erun command traces."
)

func AddTimeFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().Bool("time", false, timeFlagUsage)
}

func AuditCommand(cmd *cobra.Command, args []string) {
	ctx := clicmd.CommandContext(cmd)
	ctx.Trace("audit: " + formatAuditCommand(cmd, args))
}

func formatAuditCommand(cmd *cobra.Command, args []string) string {
	parts := strings.Fields(cmd.CommandPath())
	if len(parts) == 0 {
		parts = []string{"erun"}
	}
	parts = append(parts, changedFlagArgs(cmd)...)
	parts = append(parts, args...)
	return strings.Join(redactAuditArgs(parts), " ")
}

func changedFlagArgs(cmd *cobra.Command) []string {
	args := make([]string, 0)
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		if flag.Name == "help" || flag.Name == "verbose" {
			return
		}
		name := "--" + flag.Name
		if flag.Value.Type() == "bool" {
			args = append(args, name)
			return
		}
		args = append(args, name, flag.Value.String())
	})
	return args
}

func redactAuditArgs(args []string) []string {
	redacted := make([]string, 0, len(args))
	redactNext := false
	for _, arg := range args {
		if redactNext {
			redacted = append(redacted, "<redacted>")
			redactNext = false
			continue
		}
		if name, _, ok := strings.Cut(arg, "="); ok && isSensitiveFlag(name) {
			redacted = append(redacted, name+"=<redacted>")
			continue
		}
		redacted = append(redacted, arg)
		if isSensitiveFlag(arg) {
			redactNext = true
		}
	}
	return redacted
}

// Only flags can carry secrets; a bare word like a `token` command name is never redacted.
func isSensitiveFlag(value string) bool {
	if !strings.HasPrefix(value, "-") {
		return false
	}
	normalized := strings.ToLower(strings.TrimLeft(value, "-"))
	for _, token := range []string{"password", "passwd", "secret", "token", "apikey", "api-key", "access-key", "private-key"} {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}
