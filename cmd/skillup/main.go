package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

func main() {
	os.Exit(run())
}

func run() int {
	check := flag.Bool("check", false, "print installed skills and available updates")
	apply := flag.Bool("apply", false, "apply supported updates without opening the terminal UI")
	name := flag.String("skill", "", "limit --check or --apply to an exact skill name")
	path := flag.String("path", "", "choose an installation path when a skill name is shared")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "Usage: skillup [--check | --apply] [--skill NAME [--path PATH]]")
		flag.PrintDefaults()
	}
	flag.Parse()
	invalid := flag.NArg() != 0 || *apply && *check || *name != "" && !*apply && !*check || *path != "" && *name == ""
	flag.Visit(func(f *flag.Flag) {
		if (f.Name == "skill" || f.Name == "path") && f.Value.String() == "" {
			invalid = true
		}
	})
	if invalid {
		flag.Usage()
		return 2
	}
	cfg, err := getConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !*apply && !*check && term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if _, err := tea.NewProgram(screen{cfg: cfg, ctx: ctx, cancel: cancel, busy: true, activity: newActivity(), search: newSearch()}).Run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	label := "Checking sources…"
	if *apply {
		label = "Applying updates…"
	}
	finishProgress := textProgress(label)
	rows, err := scan(ctx, cfg, *apply, skillSelection{name: *name, path: *path})
	finishProgress()
	exit := 0
	for _, row := range rows {
		fmt.Printf("%-10s %s | %s", row.status, printable(row.name), printable(row.detail))
		if row.current != "" {
			fmt.Printf(" | %s", printable(short(row.current)))
			if row.latest != "" && row.latest != row.current {
				fmt.Printf(" -> %s", printable(short(row.latest)))
			}
		}
		if row.source != "" {
			fmt.Printf(" | %s", printable(row.source))
		}
		if row.manager != "" {
			fmt.Printf(" | Manager %s", row.manager)
		}
		for _, loc := range row.locations {
			fmt.Printf(" | %s %s", loc.label, printable(loc.path))
		}
		fmt.Println()
		if row.status == failed {
			exit = 1
		}
	}
	fmt.Println(summary(rows))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return exit
}

// Only animate on terminal stderr so text output stays suitable for scripts.
func textProgress(label string) func() {
	width, _, err := term.GetSize(os.Stderr.Fd())
	if err != nil || width < 2 {
		return func() {}
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(spinner.Dot.FPS)
		defer ticker.Stop()
		for i := 0; ; i++ {
			frame := spinner.Dot.Frames[i%len(spinner.Dot.Frames)] + " " + label
			frame = ansi.Truncate(frame, width-1, "…")
			fmt.Fprint(os.Stderr, "\r", frame)
			select {
			case <-stop:
				fmt.Fprint(os.Stderr, "\r", strings.Repeat(" ", ansi.StringWidth(frame)), "\r")
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

func printable(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
}

func short(s string) string {
	return s[:min(len(s), 10)]
}

func summary(rows []*skill) string {
	count := map[string]int{}
	for _, row := range rows {
		count[row.status]++
	}
	return fmt.Sprintf("%d skills · %d available · %d current · %d updated · %d modified · %d skipped · %d errors",
		len(rows), count[available], count[current], count[updated], count[modified], count[skipped]+count["Pinned"], count[failed])
}
