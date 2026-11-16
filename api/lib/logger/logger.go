package logger

import (
	nested "github.com/antonfisher/nested-logrus-formatter"
	log "github.com/sirupsen/logrus"
	"os"
)

const (
	DebugMode   = "debug"
	ReleaseMode = "release"
)

type Config struct {
	Level        string
	ReportCaller bool
}

// New writes only to stdout. Retention and rotation belong to the process supervisor.
func New(c *Config) *log.Logger {
	level, err := log.ParseLevel(c.Level)
	if err != nil {
		panic("invalid logger level")
	}
	logger := log.New()
	logger.SetFormatter(&nested.Formatter{
		TimestampFormat: "[2006-01-02 15:04:05]",
		NoColors:        true,
		NoFieldsColors:  true,
	})
	logger.SetOutput(os.Stdout)
	logger.SetReportCaller(c.ReportCaller)
	logger.SetLevel(level)
	return logger
}
