package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

type failingComponent struct {
	err     error
	started bool
}

func (c *failingComponent) Name() string {
	return "test-component"
}

func (c *failingComponent) Start(context.Context) error {
	c.started = true

	return c.err
}

func (c *failingComponent) Stop(context.Context) error {
	return nil
}

func TestStartPropagatesComponentFailure(t *testing.T) {
	t.Parallel()
	want := errors.New("initialization failed")
	next := &failingComponent{}
	s := &Service{Name: "engine", StartupTimeout: time.Second, Components: []Component{&failingComponent{err: want}, next}}
	if err := s.Start(); !errors.Is(err, want) {
		t.Fatalf("Start = %v; want %v", err, want)
	}
	if next.started {
		t.Fatal("continued after initialization failure")
	}
}

type orderedComponent struct {
	name   string
	events *[]string
}

func (c *orderedComponent) Name() string {
	return c.name
}

func (c *orderedComponent) Start(context.Context) error {
	*c.events = append(*c.events, "start "+c.name)

	return nil
}

func (c *orderedComponent) Stop(context.Context) error {
	*c.events = append(*c.events, "stop "+c.name)

	return nil
}

func TestStopsDependentsBeforeDependencies(t *testing.T) {
	t.Parallel()
	var events []string
	s := &Service{Name: "engine", StartupTimeout: time.Second, ShutdownTimeout: time.Second, Components: []Component{
		&orderedComponent{name: "storage", events: &events},
		&orderedComponent{name: "scheduler", events: &events},
	}}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	want := []string{"start storage", "start scheduler", "stop scheduler", "stop storage"}
	for i, event := range want {
		if events[i] != event {
			t.Fatalf("lifecycle=%v want %v", events, want)
		}
	}
}

type stopFailingComponent struct {
	orderedComponent
	cause error
}

func (c stopFailingComponent) Stop(ctx context.Context) error {
	_ = c.orderedComponent.Stop(ctx)

	return c.cause
}
func TestStopPropagatesFailureBeforeStoppingDependencies(t *testing.T) {
	t.Parallel()
	var events []string
	cause := errors.New("job did not drain")
	s := &Service{Name: "worker", ShutdownTimeout: time.Second, Components: []Component{
		&orderedComponent{name: "storage", events: &events},
		&stopFailingComponent{orderedComponent{name: "job", events: &events}, cause},
	}}
	if err := s.Stop(); !errors.Is(err, cause) {
		t.Fatalf("stop error=%v", err)
	}
	if len(events) != 1 || events[0] != "stop job" {
		t.Fatal(events)
	}
}
