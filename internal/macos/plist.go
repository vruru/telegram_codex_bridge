package macos

import (
	"strings"
)

type launchAgentSpec struct {
	Label                       string
	Program                     string
	WorkingDir                  string
	StdoutLog                   string
	StderrLog                   string
	RunAtLoad                   bool
	KeepAlive                   bool
	ProcessType                 string
	AssociatedBundleIdentifiers []string
}

func renderLaunchAgent(spec launchAgentSpec) string {
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	builder.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	builder.WriteString(`<plist version="1.0">` + "\n")
	builder.WriteString(`<dict>` + "\n")
	writePlistString(&builder, "Label", spec.Label)
	builder.WriteString("\t<key>ProgramArguments</key>\n")
	builder.WriteString("\t<array>\n")
	builder.WriteString("\t\t<string>" + xmlEscape(spec.Program) + "</string>\n")
	builder.WriteString("\t</array>\n")
	writePlistString(&builder, "WorkingDirectory", spec.WorkingDir)
	writePlistBool(&builder, "RunAtLoad", spec.RunAtLoad)
	writePlistBool(&builder, "KeepAlive", spec.KeepAlive)
	writePlistString(&builder, "ProcessType", spec.ProcessType)
	writePlistString(&builder, "StandardOutPath", spec.StdoutLog)
	writePlistString(&builder, "StandardErrorPath", spec.StderrLog)
	writePlistStringArray(&builder, "AssociatedBundleIdentifiers", spec.AssociatedBundleIdentifiers)
	builder.WriteString(`</dict>` + "\n")
	builder.WriteString(`</plist>` + "\n")
	return builder.String()
}

func writePlistString(builder *strings.Builder, key, value string) {
	builder.WriteString("\t<key>" + xmlEscape(key) + "</key>\n")
	builder.WriteString("\t<string>" + xmlEscape(value) + "</string>\n")
}

func writePlistBool(builder *strings.Builder, key string, value bool) {
	builder.WriteString("\t<key>" + xmlEscape(key) + "</key>\n")
	if value {
		builder.WriteString("\t<true/>\n")
		return
	}
	builder.WriteString("\t<false/>\n")
}

func writePlistStringArray(builder *strings.Builder, key string, values []string) {
	if len(values) == 0 {
		return
	}

	builder.WriteString("\t<key>" + xmlEscape(key) + "</key>\n")
	builder.WriteString("\t<array>\n")
	for _, value := range values {
		builder.WriteString("\t\t<string>" + xmlEscape(value) + "</string>\n")
	}
	builder.WriteString("\t</array>\n")
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}
