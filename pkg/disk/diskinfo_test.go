package disk

import (
	"errors"
	"testing"
)

func TestGetDiskSize(t *testing.T) {
	originalRunCommand := runCommand
	defer func() {
		runCommand = originalRunCommand
	}()

	tests := []struct {
		name      string
		device    string
		mockOut   string
		mockErr   error
		wantSize  uint64
		wantError bool
	}{
		{
			name:   "success parses mediasize in bytes",
			device: "/dev/ada0",
			mockOut: `"/dev/ada0"
	512             # sectorsize
	1000204886016   # mediasize in bytes (932G)
	1953525168      # mediasize in sectors
	0               # stripesize
	0               # stripeoffset
	121601          # Cylinders according to firmware.
	255             # Heads according to firmware.
	63              # Sectors according to firmware.
	ST1000DM003-1SB102    # Disk descr.
	S1DXYZAB        # Disk ident.
	Yes             # TRIM/UNMAP support
	7200            # Rotation rate in RPM
	Not_Zoned       # Zone Mode`,
			wantSize: 1000204886016,
		},
		{
			name:      "command failure",
			device:    "/dev/ada0",
			mockErr:   errors.New("diskinfo failed"),
			wantSize:  0,
			wantError: true,
		},
		{
			name:   "missing mediasize line returns zero",
			device: "/dev/ada0",
			mockOut: `"/dev/ada0"
	512             # sectorsize
	1953525168      # mediasize in sectors`,
			wantSize: 0,
		},
		{
			name:   "malformed mediasize value returns zero",
			device: "/dev/ada0",
			mockOut: `"/dev/ada0"
	not-a-number    # mediasize in bytes (932G)`,
			wantSize: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runCommand = func(command string, args ...string) (string, error) {
				if command != "/usr/sbin/diskinfo" {
					t.Fatalf("unexpected command: got %q", command)
				}
				if len(args) != 2 {
					t.Fatalf("unexpected args length: got %d, want 2", len(args))
				}
				if args[0] != "-v" {
					t.Fatalf("unexpected first arg: got %q, want %q", args[0], "-v")
				}
				if args[1] != tt.device {
					t.Fatalf("unexpected device arg: got %q, want %q", args[1], tt.device)
				}

				return tt.mockOut, tt.mockErr
			}

			got, err := GetDiskSize(tt.device)
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tt.wantSize {
				t.Fatalf("unexpected size: got %d, want %d", got, tt.wantSize)
			}
		})
	}
}
