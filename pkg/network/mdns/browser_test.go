package dnssd

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBrowse(t *testing.T) {
	testIface, _ := net.InterfaceByName("lo0")
	if testIface == nil {
		testIface, _ = net.InterfaceByName("lo")
	}
	if testIface == nil {
		t.Fatal("can not find the local interface")
	}

	localhost, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	localhost = strings.TrimSuffix(strings.Replace(localhost, " ", "-", -1), ".local") // replace spaces with dashes and remove .local suffix
	tests := []struct {
		name        string
		serviceName string
		serviceType string
		host        string
	}{
		{
			name:        "regular host",
			serviceName: "My Regular Service",
			serviceType: "_test-regular._tcp",
			host:        "My-Computer",
		},
		{
			name:        "empty host",
			serviceName: "My Empty Host Service",
			serviceType: "_test-empty._tcp",
			host:        "",
		},
		{
			name:        "ip address",
			serviceName: "My IP Service",
			serviceType: "_test-ip._tcp",
			host:        "192.168.0.1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Config{
				Name:   test.serviceName,
				Type:   test.serviceType,
				Host:   test.host,
				Port:   12334,
				Ifaces: []string{testIface.Name},
			}
			srv, err := NewService(cfg)
			if err != nil {
				t.Fatal(err)
			}
			rs, err := NewResponder()
			if err != nil {
				t.Fatal(err)
			}
			defer rs.Close()

			_, err = rs.Add(srv)
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			ready := make(chan error, 1)
			respondDone := make(chan error, 1)
			go func() {
				respondDone <- rs.(*responder).RespondReady(ctx, ready)
			}()
			defer func() {
				cancel()
				<-respondDone
			}()
			if err := <-ready; err != nil {
				t.Fatal(err)
			}

			resultChan := make(chan BrowseEntry, 1)
			lookupDone := make(chan error, 1)
			go func() {
				lookupDone <- LookupType(ctx, fmt.Sprintf("%s.local.", cfg.Type), func(entry BrowseEntry) {
					select {
					case resultChan <- entry:
					default:
					}
				}, func(entry BrowseEntry) {})
			}()
			defer func() {
				cancel()
				<-lookupDone
			}()

			select {
			case <-ctx.Done():
				t.Fatal("timeout")
			case entry := <-resultChan:
				if entry.Name != cfg.Name {
					t.Fatalf("is=%v want=%v", entry.Name, cfg.Name)
				}
				if test.name == "empty host" {
					if entry.Host != localhost {
						t.Fatalf("is=%v want=%v", entry.Host, localhost)
					}
				} else {
					if entry.Host != cfg.Host {
						t.Fatalf("is=%v want=%v", entry.Host, cfg.Host)
					}
				}
				if entry.Port != cfg.Port {
					t.Fatalf("is=%v want=%v", entry.Port, cfg.Port)
				}
			}
		})
	}
}
