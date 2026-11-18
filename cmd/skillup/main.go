package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
)

func main() {
	os.Exit(run())
}

func run() int {
	check := flag.Bool("check", false, "print installed skills and available updates")
	apply := flag.Bool("apply", false, "apply supported updates without opening the terminal UI")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "Usage: skillup [--check | --apply]")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 0 || (*apply && *check) {
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
		if _, err := tea.NewProgram(screen{cfg: cfg, ctx: ctx, cancel: cancel, busy: true}).Run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	rows, err := scan(ctx, cfg, *apply)
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
