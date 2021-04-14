package memberlist

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
)

type Logger interface {
	Debugf(format string, v ...interface{})
	Infof(format string, v ...interface{})
	Warnf(format string, v ...interface{})
	Errorf(format string, v ...interface{})
}

type loggerImpl struct {
	logger *log.Logger
}

func newLoggerImpl(output io.Writer) Logger {
	return newNamedLoggerImpl(output, "")
}

func newNamedLoggerImpl(output io.Writer, name string) Logger {
	return newNamedFlagsLoggerImpl(output, name, log.LstdFlags)
}

func newNamedFlagsLoggerImpl(output io.Writer, name string, flags int) Logger {
	var logger *log.Logger
	if output != nil {
		logger = log.New(output, name, flags)
	} else {
		logger = log.New(os.Stderr, name, flags)
	}
	return &loggerImpl{logger}
}

func (l *loggerImpl) Debugf(format string, v ...interface{}) {
	l.logger.Printf("[DEBUG] memberlist: "+format, v...)
}

func (l *loggerImpl) Infof(format string, v ...interface{}) {
	l.logger.Printf("[INFO] memberlist: "+format, v...)
}

func (l *loggerImpl) Warnf(format string, v ...interface{}) {
	l.logger.Printf("[WARN] memberlist: "+format, v...)
}

func (l *loggerImpl) Errorf(format string, v ...interface{}) {
	l.logger.Printf("[ERROR] memberlist: "+format, v...)
}

func LogAddress(addr net.Addr) string {
	if addr == nil {
		return "from=<unknown address>"
	}

	return fmt.Sprintf("from=%s", addr.String())
}

func LogStringAddress(addr string) string {
	if addr == "" {
		return "from=<unknown address>"
	}

	return fmt.Sprintf("from=%s", addr)
}

func LogConn(conn net.Conn) string {
	if conn == nil {
		return LogAddress(nil)
	}

	return LogAddress(conn.RemoteAddr())
}
