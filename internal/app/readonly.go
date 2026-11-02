package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/redis/go-redis/v9"
)

var errReadOnly = errors.New("This connection is read-only. Use a writable connection to make changes.")

// Fail closed for unknown commands, scripts and module commands. This protects
// every UI/CLI/pipeline write path; server-side ACLs remain the security boundary.
type readOnlyHook struct{}

func (readOnlyHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (readOnlyHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if !readOnlyCommand(cmd.Args()) {
			cmd.SetErr(errReadOnly)
			return errReadOnly
		}
		return next(ctx, cmd)
	}
}
func (readOnlyHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			if !readOnlyCommand(cmd.Args()) {
				for _, c := range cmds {
					c.SetErr(errReadOnly)
				}
				return errReadOnly
			}
		}
		return next(ctx, cmds)
	}
}

func readOnlyCommand(args []any) bool {
	if len(args) == 0 {
		return false
	}
	command := strings.ToUpper(fmt.Sprint(args[0]))
	sub := ""
	switch command {
	case "MEMORY", "CONFIG", "SLOWLOG", "CLIENT", "CLUSTER", "XINFO":
		if len(args) > 1 {
			sub = strings.ToUpper(fmt.Sprint(args[1]))
		}
	}
	switch command {
	case "PING", "ECHO", "HELLO", "AUTH", "SELECT", "QUIT", "READONLY", "COMMAND", "INFO", "TIME", "DBSIZE",
		"TYPE", "EXISTS", "TTL", "PTTL", "EXPIRETIME", "PEXPIRETIME", "SCAN", "GET", "MGET", "GETRANGE", "STRLEN", "GETBIT", "BITCOUNT", "BITPOS",
		"HGET", "HMGET", "HGETALL", "HEXISTS", "HLEN", "HKEYS", "HVALS", "HSCAN", "HRANDFIELD", "HSTRLEN",
		"LRANGE", "LINDEX", "LLEN", "LPOS", "SCARD", "SSCAN", "SISMEMBER", "SMISMEMBER", "SMEMBERS", "SRANDMEMBER", "SDIFF", "SINTER", "SUNION",
		"ZCARD", "ZCOUNT", "ZLEXCOUNT", "ZRANGE", "ZREVRANGE", "ZRANGEBYSCORE", "ZREVRANGEBYSCORE", "ZRANGEBYLEX", "ZREVRANGEBYLEX", "ZRANK", "ZREVRANK", "ZSCORE", "ZMSCORE", "ZSCAN", "ZRANDMEMBER",
		"XLEN", "XRANGE", "XREVRANGE", "XPENDING", "XREAD", "SUBSCRIBE", "PSUBSCRIBE", "UNSUBSCRIBE", "PUNSUBSCRIBE", "PUBSUB",
		"JSON.GET", "JSON.TYPE", "JSON.OBJKEYS", "JSON.OBJLEN", "JSON.ARRLEN", "JSON.STRLEN":
		return true
	case "MEMORY":
		return sub == "USAGE" || sub == "STATS"
	case "CONFIG":
		return sub == "GET"
	case "SLOWLOG":
		return sub == "GET" || sub == "LEN"
	case "CLIENT":
		return sub == "LIST" || sub == "INFO" || sub == "ID" || sub == "GETNAME" || sub == "SETNAME" || sub == "SETINFO" || sub == "MAINT_NOTIFICATIONS"
	case "CLUSTER":
		return sub == "INFO" || sub == "NODES" || sub == "SLOTS" || sub == "SHARDS" || sub == "KEYSLOT" || sub == "MYID"
	case "XINFO":
		return sub == "STREAM" || sub == "GROUPS" || sub == "CONSUMERS"
	}
	return false
}

func (s *session) writable() bool {
	if s == nil || s.closed {
		return false
	}
	if s.cfg.ReadOnly {
		s.setStatusErr(errReadOnly)
		return false
	}
	if s.dbSwitching || s.keyBusy || s.ioBusy {
		s.setStatusErr(localError("Wait for the current transfer or deletion to finish."))
		return false
	}
	return true
}

func (a *App) connectionBadges(c *ui.Context, pal *palette, o ConnectionOptions) {
	if o.Environment != "" && o.Environment != "Unspecified" {
		color := pal.muted
		if o.Environment == "Production" {
			color = pal.danger
		}
		smallBadge(c, a.tr(o.Environment), pal).TextColor(color)
	}
	if o.ReadOnly {
		smallBadge(c, a.tr("Read-only"), pal).TextColor(pal.accent)
	}
}

func (s *session) connectionContext() string {
	name, address := s.name(), s.cfg.Addr
	switch s.cfg.mode() {
	case "cluster":
		address = strings.Join(s.cfg.ClusterAddrs, ", ")
	case "sentinel":
		address = s.cfg.MasterName + " @ " + strings.Join(s.cfg.SentinelAddrs, ", ")
	}
	if address != "" && address != name {
		name += " (" + address + ")"
	}
	text := s.app.tr("Connection: %s · Database: %s", name, s.cfg.dbLabel(s.db))
	if s.cfg.Environment != "" && s.cfg.Environment != "Unspecified" {
		text += " · " + s.app.tr(s.cfg.Environment)
	}
	return text
}
