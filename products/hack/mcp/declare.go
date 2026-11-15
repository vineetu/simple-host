// Package hackmcp declares Simple Hack's AI tools and instructions. The tool
// server (internal/mcp) receives them as one Contribution.
package hackmcp

import "github.com/vsriram/simple-host/internal/mcp"

// decl is one tool as this package writes it: the listed fields, the
// declaration options and the function that runs it.
type decl struct {
	Name, Title, Description               string
	InputSchema, OutputSchema, Annotations map[string]any
	policy                                 toolPolicy
	run                                    func(c *mcp.Call, args map[string]any) (mcp.Output, error)
}

func (d decl) tool() mcp.Tool {
	tool := mcp.Tool{Name: d.Name, Title: d.Title, Description: d.Description, InputSchema: d.InputSchema, OutputSchema: d.OutputSchema, Annotations: d.Annotations}
	if d.run != nil {
		tool = mcp.WithRun(tool, d.run)
	}
	return withToolPolicy(tool, d.policy)
}

func declare(decls []decl) []mcp.Tool {
	tools := make([]mcp.Tool, 0, len(decls))
	for _, d := range decls {
		tools = append(tools, d.tool())
	}
	return tools
}

// toolPolicy names the declaration options Hack's tools use.
type toolPolicy struct {
	memberResult, organiserResult                                        string
	judgeParameter, includeEmails, recordsPins, screenshot, optionalBody bool
	connectorHidden                                                      bool
	analytics                                                            analyticsPolicy
}

type analyticsPolicy struct {
	secretText, secretKey, secretURL, fileResult, fileArguments, applicationAnswers bool
}

func withToolPolicy(tool mcp.Tool, p toolPolicy) mcp.Tool {
	var options []mcp.Policy
	add := func(on bool, option mcp.Policy) {
		if on {
			options = append(options, option)
		}
	}
	add(p.memberResult != "", mcp.MemberResult(p.memberResult))
	add(p.organiserResult != "", mcp.OrganiserResult(p.organiserResult))
	add(p.judgeParameter, mcp.JudgeParameter())
	add(p.includeEmails, mcp.IncludeEmails())
	add(p.recordsPins, mcp.RecordsPins())
	add(p.screenshot, mcp.Screenshot())
	add(p.optionalBody, mcp.OptionalBody())
	add(p.connectorHidden, mcp.ConnectorHidden())
	var analytics []mcp.AnalyticsOption
	a := p.analytics
	for _, option := range []struct {
		on     bool
		option mcp.AnalyticsOption
	}{
		{a.secretText, mcp.SecretText()}, {a.secretKey, mcp.SecretKey()}, {a.secretURL, mcp.SecretURL()},
		{a.fileResult, mcp.FileResult()}, {a.fileArguments, mcp.FileArguments()}, {a.applicationAnswers, mcp.ApplicationAnswers()},
	} {
		if option.on {
			analytics = append(analytics, option.option)
		}
	}
	add(len(analytics) > 0, mcp.Analytics(analytics...))
	return mcp.WithPolicy(tool, options...)
}
