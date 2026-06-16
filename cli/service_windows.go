//go:build windows

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func isWindowsService() (bool, error) {
	return svc.IsWindowsService()
}

type sonicService struct {
	stopChan         chan struct{}
	shutdownDoneChan chan struct{}
}

func (m *sonicService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	// Sinaliza que iniciou com sucesso
	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				close(m.stopChan)
				// Aguarda o encerramento gracioso terminar no root.go
				if m.shutdownDoneChan != nil {
					<-m.shutdownDoneChan
				}
				changes <- svc.Status{State: svc.Stopped}
				return
			}
		}
	}
}

// runServiceInSCM inicia o controle de loop de serviço do Windows.
func runServiceInSCM(stopChan chan struct{}, shutdownDoneChan chan struct{}) {
	s := &sonicService{stopChan: stopChan, shutdownDoneChan: shutdownDoneChan}
	_ = svc.Run("SonicEdgeEngine", s)
}

// ManageService gerencia a instalação e controle do serviço via CLI.
func ManageService(action string) {
	const svcName = "SonicEdgeEngine"
	const displayName = "Sonic Edge Proxy Engine"
	const desc = "Multi-Language, Multi-Protocol Edge Proxy Engine with eBPF and Semantic AI Caching."

	var err error
	switch action {
	case "install":
		err = installService(svcName, displayName, desc)
		if err == nil {
			fmt.Println("[SUCCESS] Service installed successfully.")
		}
	case "uninstall":
		err = uninstallService(svcName)
		if err == nil {
			fmt.Println("[SUCCESS] Service uninstalled successfully.")
		}
	case "start":
		err = startService(svcName)
		if err == nil {
			fmt.Println("[SUCCESS] Service started successfully.")
		}
	case "stop":
		err = stopService(svcName)
		if err == nil {
			fmt.Println("[SUCCESS] Service stopped successfully.")
		}
	default:
		fmt.Printf("Unknown service action: %s\n", action)
		os.Exit(1)
	}

	if err != nil {
		fmt.Printf("[ERROR] Service action '%s' failed: %v\n", action, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func installService(name, displayName, desc string) error {
	exepath, err := os.Executable()
	if err != nil {
		return err
	}
	// Converter para caminho absoluto completo para evitar falhas do SCM do Windows
	exepath, err = filepath.Abs(exepath)
	if err != nil {
		return err
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err == nil {
		s.Close()
		return fmt.Errorf("service %s already exists", name)
	}

	s, err = m.CreateService(name, exepath, mgr.Config{
		StartType:   mgr.StartAutomatic,
		DisplayName: displayName,
		Description: desc,
	})
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}
	defer s.Close()
	return nil
}

func uninstallService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("service %s is not installed", name)
	}
	defer s.Close()

	err = s.Delete()
	if err != nil {
		return fmt.Errorf("failed to delete service: %w", err)
	}
	return nil
}

func startService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()

	return s.Start()
}

func stopService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer s.Disconnect()

	srv, err := s.OpenService(name)
	if err != nil {
		return err
	}
	defer srv.Close()

	status, err := srv.Control(svc.Stop)
	if err != nil {
		return err
	}

	// Aguarda o término com timeout de segurança de 15 segundos
	startTime := time.Now()
	for status.State != svc.Stopped {
		if time.Since(startTime) > 15*time.Second {
			return fmt.Errorf("timeout waiting for service to stop")
		}
		time.Sleep(200 * time.Millisecond)
		status, err = srv.Query()
		if err != nil {
			return err
		}
	}
	return nil
}
