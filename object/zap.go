package object

import "go.uber.org/zap"

var (
	logger *zap.Logger
)

func init() {
	logger, _ = zap.NewDevelopment()
}

func GetLogger() *zap.Logger {
	return logger
}
