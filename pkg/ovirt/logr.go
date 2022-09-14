package ovirt

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	ovirtclientlog "github.com/ovirt/go-ovirt-client-log/v3"
	"k8s.io/klog/v2/klogr"
)

type KLogr struct {
	logger logr.Logger

	VDebug   int
	VInfo    int
	VWarning int
}

func (g *KLogr) Debugf(format string, args ...interface{}) {
	g.logger.V(g.VDebug).Info(fmt.Sprintf(format, args...))
}

func (g *KLogr) Infof(format string, args ...interface{}) {
	g.logger.V(g.VInfo).Info(fmt.Sprintf(format, args...))
}

func (g *KLogr) Warningf(format string, args ...interface{}) {
	g.logger.V(g.VWarning).Info(fmt.Sprintf(format, args...))
}

func (g *KLogr) Errorf(format string, args ...interface{}) {
	g.logger.Error(fmt.Errorf(format, args...), "error")
}

func (g *KLogr) WithContext(ctx context.Context) ovirtclientlog.Logger {
	return g
}

func (g *KLogr) WithVDebug(level int) *KLogr {
	g.VDebug = level
	return g
}

func (g *KLogr) WithVInfo(level int) *KLogr {
	g.VInfo = level
	return g
}

func (g *KLogr) WithVWarning(level int) *KLogr {
	g.VWarning = level
	return g
}

func NewKLogr(names ...string) *KLogr {
	// offset the call stack by 1 frame to get the site information of the original caller
	logger := klogr.New().WithCallDepth(1)
	for _, name := range names {
		logger = logger.WithName(name)
	}

	return &KLogr{
		logger:   logger,
		VDebug:   5,
		VInfo:    0,
		VWarning: 0,
	}
}
