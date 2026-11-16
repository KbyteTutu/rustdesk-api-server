package config

type Logger struct {
	Level        string
	ReportCaller bool `mapstructure:"report-caller"`
}
