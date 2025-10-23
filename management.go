package xconn

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	ManagementProcedureEnableStats      = "io.xconn.management.stats.enable"
	ManagementProcedureDisableStats     = "io.xconn.management.stats.disable"
	ManagementProcedureSetStatsInterval = "io.xconn.management.stats.interval.set"
	ManagementProcedureStatsStatus      = "io.xconn.management.stats.status"

	ManagementProcedureSetLogLevel = "io.xconn.management.loglevel.set"
)

type management struct {
	session      *Session
	stopMemStats chan struct{}
	memRunning   bool
	interval     time.Duration
}

func newManagementAPI(session *Session) *management {
	return &management{
		session:  session,
		interval: time.Second,
	}
}

func (m *management) start() error {
	for uri, handler := range map[string]InvocationHandler{
		ManagementProcedureEnableStats:      m.handleEnableStats,
		ManagementProcedureDisableStats:     m.handleDisableStats,
		ManagementProcedureSetStatsInterval: m.handleChangeInterval,
		ManagementProcedureStatsStatus:      m.handleStatsStatus,
		ManagementProcedureSetLogLevel:      m.handleSetLogLevel,
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

func (m *management) handleEnableStats(_ context.Context, invocation *Invocation) *InvocationResult {
	interval, err := invocation.ArgInt64(0)
	if err != nil {
		interval = 1000 // default 1s
	}

	err = m.startMemoryLogging(time.Duration(interval) * time.Millisecond)
	if err != nil {
		return NewInvocationError("wamp.error.internal_error", err.Error())
	}
	return NewInvocationResult()
}

func (m *management) handleDisableStats(_ context.Context, _ *Invocation) *InvocationResult {
	m.stopMemoryLogging()
	return NewInvocationResult()
}

func (m *management) handleChangeInterval(_ context.Context, invocation *Invocation) *InvocationResult {
	newIntervalMS, err := invocation.ArgInt64(0)
	if err != nil {
		return NewInvocationError("wamp.error.invalid_argument", "interval must be a positive integer (milliseconds)")
	}

	newInterval := time.Duration(newIntervalMS) * time.Millisecond
	m.interval = newInterval

	if m.memRunning {
		log.Infof("Changing memory logging interval to %v", newInterval)
		m.stopMemoryLogging()
		_ = m.startMemoryLogging(newInterval)
	} else {
		log.Infof("Updated memory logging interval to %v (will apply when started)", newInterval)
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
