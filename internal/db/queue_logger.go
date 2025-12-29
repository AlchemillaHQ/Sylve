package db

import "github.com/rs/zerolog"

type zerologJobLogger struct {
	Logger zerolog.Logger
}

func (l zerologJobLogger) Info(msg string, args ...any) {
	evt := l.Logger.Debug()
	for i := 0; i < len(args)-1; i += 2 {
		key, _ := args[i].(string)
		evt = evt.Interface(key, args[i+1])
	}
	evt.Msg(msg)
}

func (l zerologJobLogger) Error(msg string, args ...any) {
	evt := l.Logger.Error()
	for i := 0; i < len(args)-1; i += 2 {
		key, _ := args[i].(string)
		evt = evt.Interface(key, args[i+1])
	}
	evt.Msg(msg)
}

func (l zerologJobLogger) Warn(msg string, args ...any) {
	evt := l.Logger.Warn()
	for i := 0; i < len(args)-1; i += 2 {
		key, _ := args[i].(string)
		evt = evt.Interface(key, args[i+1])
	}
	evt.Msg(msg)
}
