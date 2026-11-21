//go:build windows

package main

import (
	"context"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/svc"
)

// windowsServiceName must match the installer's sc.exe create call.
const windowsServiceName = "vaultix-agent"

// windowsLogFile is the service log sink (services have no stderr).
func windowsLogFile() string {
	if dir := os.Getenv("ProgramData"); dir != "" {
		return filepath.Join(dir, "Vaultix", "agent.log")
	}
	return `C:\ProgramData\Vaultix\agent.log`
}

type windowsService struct {
	configFlag string
}

func (s *windowsService) Execute(_ []string, req <-chan svc.ChangeRequest, stat chan<- svc.Status) (bool, uint32) {
	stat <- svc.Status{State: svc.StartPending}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	logOut, err := openWindowsLog()
	if err != nil {
		return true, 1
	}
	defer func() { _ = logOut.Close() }()
	done := make(chan error, 1)
	go func() { done <- runAgentDaemon(ctx, s.configFlag, logOut) }()
	stat <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				stat <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				stop()
				stat <- svc.Status{State: svc.StopPending}
				<-done
				return false, 0
			}
		case err := <-done:
			if err != nil {
				return true, 1
			}
			return false, 0
		}
	}
}

func openWindowsLog() (*os.File, error) {
	path := windowsLogFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
}

// maybeRunService runs the SCM service loop when started by Windows
// services, reporting true. Console runs return false (normal CLI).
func maybeRunService() bool {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return false
	}
	configFlag := ""
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config":
			if i+1 < len(args) {
				configFlag = args[i+1]
			}
		}
	}
	if err := svc.Run(windowsServiceName, &windowsService{configFlag: configFlag}); err != nil {
		if f, ferr := openWindowsLog(); ferr == nil {
			_, _ = f.WriteString("service run failed: " + err.Error() + "\n")
			_ = f.Close()
		}
	}
	return true
}
