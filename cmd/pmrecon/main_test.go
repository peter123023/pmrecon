package main

import (
	"flag"
	"testing"
	"time"
)

func newFlags() (*flag.FlagSet, *float64, *time.Duration, *int, *bool) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	tol := fs.Float64("tol", 0.001, "")
	interval := fs.Duration("interval", time.Minute, "")
	rounds := fs.Int("rounds", 0, "")
	all := fs.Bool("all", false, "")
	return fs, tol, interval, rounds, all
}

func TestSplitArgsAllowsFlagsAfterWallet(t *testing.T) {
	fs, _, interval, rounds, _ := newFlags()
	args := splitArgs(fs, []string{"0xabc", "-rounds", "2", "-interval", "5s"})
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	if fs.NArg() != 1 || fs.Arg(0) != "0xabc" {
		t.Fatalf("positional args = %v", fs.Args())
	}
	if *rounds != 2 || *interval != 5*time.Second {
		t.Errorf("rounds=%d interval=%s", *rounds, *interval)
	}
}

func TestSplitArgsKeepsBoolFlagsBare(t *testing.T) {
	fs, _, _, _, all := newFlags()
	args := splitArgs(fs, []string{"-all", "0xabc"})
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	if !*all || fs.NArg() != 1 || fs.Arg(0) != "0xabc" {
		t.Fatalf("all=%v args=%v", *all, fs.Args())
	}
}

func TestSplitArgsSupportsEqualsForm(t *testing.T) {
	fs, tol, _, _, _ := newFlags()
	args := splitArgs(fs, []string{"0xabc", "-tol=0.5"})
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	if *tol != 0.5 {
		t.Errorf("tol = %v", *tol)
	}
	if fs.NArg() != 1 || fs.Arg(0) != "0xabc" {
		t.Errorf("args = %v", fs.Args())
	}
}

func TestSplitArgsTreatsDashAsPositional(t *testing.T) {
	fs, _, _, _, _ := newFlags()
	args := splitArgs(fs, []string{"-", "0xabc"})
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	if fs.NArg() != 2 {
		t.Fatalf("args = %v", fs.Args())
	}
}
