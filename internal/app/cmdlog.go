package app

// A go-redis hook that keeps a ring of the commands this app ran.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// CommandEntry is one Redis command and how long it took.
type CommandEntry struct {
	At   time.Time
	Name string
	Args []string
	Dur  time.Duration
	Err  string
}

// Command is the command and its arguments, with secrets hidden.
func (e CommandEntry) Command() string {
	name := strings.ToUpper(e.Name)
	args := e.Args
	if sensitiveCommand(e.Name, args) {
		args = []string{"[redacted]"}
	}
	if len(args) == 0 {
		return name
	}
	return name + " " + strings.Join(args, " ")
}

func sensitiveCommand(name string, args []string) bool {
	switch strings.ToUpper(name) {
	case "AUTH", "HELLO", "ACL", "MIGRATE":
		return true
	case "CONFIG", "SENTINEL":
		return len(args) > 0 && strings.EqualFold(args[0], "SET")
	}
	return false
}

// Clone shortened strings so a small log entry cannot retain a huge value.
func limitedText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return strings.Clone(s[:limit]) + "…"
}

// commandHook records every command run through a Store's client.
type commandHook struct {
	record func(CommandEntry)
}

func (h *commandHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *commandHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmd)
		h.entry(cmd, time.Since(start), err)
		return err
	}
}

func (h *commandHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmds)
		dur := time.Since(start)
		for _, cmd := range cmds {
			h.entry(cmd, dur, cmd.Err())
		}
		return err
	}
}

func (h *commandHook) entry(cmd redis.Cmder, dur time.Duration, err error) {
	if h == nil || h.record == nil {
		return
	}
	e := CommandEntry{At: time.Now(), Name: cmd.Name(), Dur: dur}
	args := cmd.Args()
	for _, arg := range args[min(1, len(args)):] {
		if len(e.Args) == 32 {
			e.Args = append(e.Args, "…")
			break
		}
		var value string
		switch v := arg.(type) {
		case string:
			value = limitedText(v, 300)
		case []byte:
			value = string(v[:min(len(v), 300)])
		default:
			value = limitedText(fmt.Sprint(v), 300)
		}
		e.Args = append(e.Args, value)
	}
	sensitive := sensitiveCommand(e.Name, e.Args)
	if sensitive {
		e.Args = []string{"[redacted]"}
	}
	if err != nil {
		e.Err = limitedText(err.Error(), 1024)
		if sensitive {
			e.Err = "command failed"
		}
	}
	h.record(e)
}
