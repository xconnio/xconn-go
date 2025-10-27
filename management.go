package xconn

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/xconnio/wampproto-go/serializers"
)

const (
	ManagementProcedureStatsStatusSet = "io.xconn.mgmt.stats.status.set"
	ManagementProcedureStatsStatusGet = "io.xconn.mgmt.stats.status.get"

	ManagementTopicStats = "io.xconn.mgmt.stats.on_update"

	ManagementProcedureSetLogLevel = "io.xconn.mgmt.log.level.set"
	ManagementProcedureGetLogLevel = "io.xconn.mgmt.log.level.get"

	ManagementProcedureListRealms  = "io.xconn.mgmt.realm.list"
	ManagementProcedureListSession = "io.xconn.mgmt.session.list"
)

type management struct {
	session      *Session
	router       *Router
	stopMemStats chan struct{}
	memRunning   bool
	interval     time.Duration
}

func newManagementAPI(session *Session, router *Router) *management {
	return &management{
		session:  session,
		interval: time.Second,
		router:   router,
	}
}

func (m *management) start() error {
	for uri, handler := range map[string]InvocationHandler{
		ManagementProcedureStatsStatusSet: m.handleSetStatsStatus,
		ManagementProcedureStatsStatusGet: m.handleStatsStatus,
		ManagementProcedureSetLogLevel:    m.handleSetLogLevel,
		ManagementProcedureGetLogLevel:    m.handleGetLogLevel,
		ManagementProcedureListRealms:     m.handleListRealms,
		ManagementProcedureListSession:    m.handleListSession,
	} {
		response := m.session.Register(uri, handler).Do()
		if response.Err != nil {
			return response.Err
		}
	}
	return nil
}

// startMemoryLogging starts the periodic memory logging.
func (m *management) startMemoryLogging(interval time.Duration) error {
	if m.memRunning {
		return fmt.Errorf("memory logging is already running")
	}

	m.interval = interval
	m.stopMemStats = make(chan struct{})
	m.memRunning = true

	go func() {
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()

		var memStats runtime.MemStats
		for {
			select {
			case <-ticker.C:
				runtime.ReadMemStats(&memStats)
				log.Infof("MemStats: Alloc=%d Mallocs=%d Frees=%d NumGC=%d",
					memStats.Alloc, memStats.Mallocs, memStats.Frees, memStats.NumGC)
				// TODO: Publish only if there are subscribers
				m.session.Publish(ManagementTopicStats).Arg(map[string]any{
					"alloc":  memStats.Alloc,
					"malloc": memStats.Mallocs,
					"frees":  memStats.Frees,
					"num_gc": memStats.NumGC,
				}).Do()
			case <-m.stopMemStats:
				log.Infoln("Stopped memory logging")
				return
			}
		}
	}()
	log.Infof("Started memory logging (interval=%v)", m.interval)
	return nil
}

// stopMemoryLogging stops the memory logging.
func (m *management) stopMemoryLogging() {
	if !m.memRunning {
		log.Warn("Memory logging is not running")
		return
	}

	close(m.stopMemStats)
	m.memRunning = false
}

func (m *management) handleSetStatsStatus(_ context.Context, invocation *Invocation) *InvocationResult {
	enable := invocation.KwargBoolOr("enable", false)
	disable := invocation.KwargBoolOr("disable", false)

	if enable && disable {
		return NewInvocationError("wamp.error.invalid_argument", "only one of 'enable' or 'disable' can be true")
	}

	intervalMS, err := invocation.KwargInt64("interval")
	intervalProvided := err == nil

	if intervalMS == 0 {
		if m.interval > 0 {
			intervalMS = int64(m.interval / time.Millisecond)
		} else {
			intervalMS = 1000 // default 1s
		}
	}
	interval := time.Duration(intervalMS) * time.Millisecond

	if intervalProvided {
		m.interval = interval
	}

	switch {
	case enable:
		if m.memRunning {
			m.stopMemoryLogging()
		}
		if err := m.startMemoryLogging(interval); err != nil {
			return NewInvocationError("wamp.error.internal_error", err.Error())
		}

	case disable:
		if m.memRunning {
			m.stopMemoryLogging()
		}

	case intervalProvided:
		if m.memRunning {
			log.Infof("Changing memory logging interval to %v", interval)
			m.stopMemoryLogging()
			_ = m.startMemoryLogging(interval)
		} else {
			log.Infof("Updated memory logging interval to %v (will apply when started)", interval)
		}

	default:
		return NewInvocationError("wamp.error.invalid_argument", "no valid kwargs provided (enable, disable, or interval)")
	}

	return NewInvocationResult()
}

func (m *management) handleStatsStatus(_ context.Context, _ *Invocation) *InvocationResult {
	status := map[string]any{
		"running":  m.memRunning,
		"interval": m.interval / time.Millisecond,
	}
	return NewInvocationResult(status)
}

func (m *management) handleSetLogLevel(_ context.Context, inv *Invocation) *InvocationResult {
	logLevel, err := inv.ArgString(0)
	if err != nil {
		return NewInvocationError("wamp.error.invalid_argument", err.Error())
	}

	level, err := log.ParseLevel(strings.ToLower(logLevel))
	if err != nil {
		return NewInvocationError("wamp.error.invalid_argument", err.Error())
	}

	log.SetLevel(level)
	return NewInvocationResult()
}

func (m *management) handleGetLogLevel(_ context.Context, _ *Invocation) *InvocationResult {
	return NewInvocationResult(log.GetLevel().String())
}

func (m *management) handleListRealms(_ context.Context, _ *Invocation) *InvocationResult {
	var realmNames []string
	m.router.realms.Range(func(name string, _ *Realm) bool {
		realmNames = append(realmNames, name)
		return true
	})
	return NewInvocationResult(realmNames)
}

func (m *management) handleListSession(_ context.Context, inv *Invocation) *InvocationResult {
	realm, err := inv.ArgString(0)
	if err != nil {
		return NewInvocationError("wamp.error.invalid_argument", err.Error())
	}

	rlm, ok := m.router.realms.Load(realm)
	if !ok {
		return NewInvocationError("wamp.error.not_found", "no such realm")
	}

	var sessions []map[string]any
	rlm.clients.Range(func(key uint64, value BaseSession) bool {
		sessions = append(sessions, sessionToMap(value))
		return true
	})

	limit := inv.KwargInt64Or("limit", 50)
	offset := inv.KwargInt64Or("offset", 0)

	paged, total := paginateList(sessions, offset, limit)

	return &InvocationResult{
		Args: []any{paged},
		Kwargs: map[string]any{
			"total":  total,
			"offset": offset,
			"limit":  limit,
		},
	}
}

func paginateList[T any](items []T, offset, limit int64) ([]T, int64) {
	total := int64(len(items))

	if limit <= 0 {
		return items, total
	}

	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}

	return items[offset:end], total
}

var serializerNameBySerializer = map[Serializer]string{ //nolint:gochecknoglobals
	&serializers.JSONSerializer{}:    "json",
	&serializers.CBORSerializer{}:    "cbor",
	&serializers.MsgPackSerializer{}: "msgpack",
}

func sessionToMap(s BaseSession) map[string]any {
	serializer, ok := serializerNameBySerializer[s.Serializer()]
	if !ok {
		serializer = "unknown"
	}

	return map[string]any{
		"authid":     s.AuthID(),
		"authrole":   s.AuthRole(),
		"sessionID":  s.ID(),
		"serializer": serializer,
	}
}
