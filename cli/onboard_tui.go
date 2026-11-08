package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/lengzhao/AgentDock/agent"
)

var onboardTitleStyle = lipgloss.NewStyle().Bold(true)

func onboardWelcome(resumeFrom string) error {
	body := "This wizard configures AgentDock end to end: agents, platform, background service, and an optional live message test."
	if resumeFrom != "" {
		body += fmt.Sprintf("\n\nResuming from step %q.", resumeFrom)
	}
	return huh.NewForm(
		huh.NewGroup(huh.NewNote().
			Title("AgentDock onboard").
			Description(body + "\n\nPress Continue when ready. Platform login may print a QR code in this terminal afterward."),
		),
	).WithShowHelp(true).Run()
}

func onboardPickAgents(existing []string) ([]string, error) {
	if len(existing) > 0 {
		var keep bool
		err := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title("Agents").
					Description(fmt.Sprintf("Current: %s", strings.Join(existing, ", "))).
					Affirmative("Keep").
					Negative("Choose again").
					Value(&keep),
			),
		).Run()
		if err != nil {
			return nil, err
		}
		if keep {
			return existing, nil
		}
	}
	ids := agent.PresetIDs()
	options := make([]huh.Option[string], 0, len(ids))
	for _, id := range ids {
		label := id
		if p, ok := agent.PresetByID(id); ok && p.LoginHint != "" {
			label = id + " — " + p.LoginHint
		}
		options = append(options, huh.NewOption(label, id))
	}
	var picked []string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Choose agents").
				Description("Pick at least one. The first selection becomes the default.").
				Options(options...).
				Value(&picked).
				Validate(func(v []string) error {
					if len(v) == 0 {
						return fmt.Errorf("select at least one agent")
					}
					return nil
				}),
		),
	).Run()
	return picked, err
}

func onboardConfirmAgentLogin(hints string) error {
	var ok bool
	return huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("Log in to your agents").
				Description(hints),
			huh.NewConfirm().
				Title("Ready").
				Description("Agent login is not verified automatically; only the final message test proves the full path.").
				Affirmative("I have logged in").
				Negative("Not yet").
				Value(&ok).
				Validate(func(b bool) error {
					if !b {
						return fmt.Errorf("log in to the agents above, then run agentdock onboard again")
					}
					return nil
				}),
		),
	).Run()
}

func onboardPickPlatform(current string) (string, error) {
	if strings.TrimSpace(current) != "" {
		var keep bool
		err := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title("Platform").
					Description(fmt.Sprintf("Current primary platform: %s", current)).
					Affirmative("Keep").
					Negative("Change").
					Value(&keep),
			),
		).Run()
		if err != nil {
			return "", err
		}
		if keep {
			return current, nil
		}
	}
	const (
		lark   = "lark"
		feishu = "feishu"
		weixin = "weixin"
	)
	var kind string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Messaging platform").
				Options(
					huh.NewOption("Lark (international)", lark),
					huh.NewOption("Feishu (China)", feishu),
					huh.NewOption("Weixin", weixin),
				).
				Value(&kind),
		),
	).Run()
	return kind, err
}

func onboardConfirmVerify(skip *bool) error {
	var runTest bool = true
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("End-to-end test").
				Description("After the background service starts, you will send a one-time token to the bot in DM and wait for the reply. This is the only check that proves messages work.").
				Affirmative("Run the test").
				Negative("Skip for now").
				Value(&runTest),
		),
	).Run()
	if err != nil {
		return err
	}
	*skip = !runTest
	return nil
}

func onboardDone() error {
	return huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title(onboardTitleStyle.Render("Onboard complete")).
				Description("The background service should be running. Use `agentdock status` and `agentdock doctor` anytime.\n\nChat with your bot on the configured platform."),
		),
	).Run()
}
