package app

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestMainThreadIPCHandlerQueuesCommand(t *testing.T) {
	wantArgs := []string{":reload"}
	wantErr := errors.New("command result")
	queued := make(chan func(), 1)
	called := make(chan []string, 1)

	handler := mainThreadIPCHandler{
		queue: func(callback func()) {
			queued <- callback
		},
		command: func(args []string) error {
			called <- args
			return wantErr
		},
	}

	result := make(chan error, 1)
	go func() {
		result <- handler.Command(wantArgs)
	}()

	var callback func()
	select {
	case callback = <-queued:
	case <-time.After(time.Second):
		t.Fatal("IPC command was not queued")
	}

	select {
	case <-called:
		t.Fatal("IPC command ran before the main-loop callback")
	default:
	}

	callback()

	select {
	case gotArgs := <-called:
		if !reflect.DeepEqual(gotArgs, wantArgs) {
			t.Fatalf("command args = %q, want %q", gotArgs, wantArgs)
		}
	case <-time.After(time.Second):
		t.Fatal("queued IPC command did not run")
	}

	select {
	case gotErr := <-result:
		if !errors.Is(gotErr, wantErr) {
			t.Fatalf("command error = %v, want %v", gotErr, wantErr)
		}
	case <-time.After(time.Second):
		t.Fatal("IPC handler did not return the command result")
	}
}

func TestIPCHandlerUsesMainThreadQueue(t *testing.T) {
	if _, ok := IPCHandler().(mainThreadIPCHandler); !ok {
		t.Fatal("IPCHandler bypasses mainThreadIPCHandler")
	}
}
