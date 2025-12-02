package zfs

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/pkg/utils"
)

const (
	_                     = iota
	BlockDevice InodeType = iota
	CharacterDevice
	Directory
	Door
	NamedPipe
	SymbolicLink
	EventPort
	Socket
	File
)

const (
	_                  = iota
	Removed ChangeType = iota
	Created
	Modified
	Renamed
)

/*
{
   "output_version":{
      "command":"zfs get",
      "vers_major":0,
      "vers_minor":1
   },
   "datasets":{
      "zroot/sylve":{
         "name":"zroot/sylve",
         "type":"FILESYSTEM",
         "pool":"zroot",
         "createtxg":"780440",
         "properties":{
            "mountpoint":{
               "value":"/zroot/sylve",
               "source":{
                  "type":"INHERITED",
                  "data":"zroot"
               }
            }
         }
      }
   }
}
*/

type DatasetType string

const (
	Filesystem DatasetType = "FILESYSTEM"
	Snapshot   DatasetType = "SNAPSHOT"
	Volume     DatasetType = "VOLUME"
)

type JSONOutputVersion struct {
	Command   string `json:"command"`
	VersMajor int    `json:"vers_major"`
	VersMinor int    `json:"vers_minor"`
}

type JSONProperty struct {
	Value  string          `json:"value"`
	Source JSONPropertySrc `json:"source"`
}

type JSONPropertySrc struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

var (
	dsPropList           = []string{"name", "origin", "used", "avail", "recordsize", "mountpoint", "compression", "type", "volsize", "quota", "referenced", "written", "logicalused", "usedbydataset", "guid", "mounted", "checksum", "aclmode", "aclinherit", "acltype", "primarycache", "volmode"}
	zpoolPropList        = []string{"name", "health", "allocated", "size", "free", "readonly", "dedupratio", "fragmentation", "freeing", "leaked", "guid"}
	zpoolPropListOptions = strings.Join(zpoolPropList, ",")
	zpoolArgs            = []string{"get", "-Hp", zpoolPropListOptions}
	zdbArgs              = []string{"-C"}

	zpoolVdevArgs   = []string{"list", "-HPpv"}
	zpoolStatusArgs = []string{"status", "-p", "-P", "-v"}
)

var changeTypeMap = map[string]ChangeType{
	"-": Removed,
	"+": Created,
	"M": Modified,
	"R": Renamed,
}

var inodeTypeMap = map[string]InodeType{
	"B": BlockDevice,
	"C": CharacterDevice,
	"/": Directory,
	">": Door,
	"|": NamedPipe,
	"@": SymbolicLink,
	"P": EventPort,
	"=": Socket,
	"F": File,
}

var referenceCountRegex = regexp.MustCompile(`\(([+-]\d+?)\)`)

func parseInodeChange(line []string) (*InodeChange, error) {
	llen := len(line)
	if llen < 1 {
		return nil, fmt.Errorf("empty line passed")
	}

	changeType := changeTypeMap[line[0]]
	if changeType == 0 {
		return nil, fmt.Errorf("unknown change type '%s'", line[0])
	}

	switch changeType {
	case Renamed:
		if llen != 4 {
			return nil, fmt.Errorf("mismatching number of fields: expect 4, got: %d", llen)
		}
	case Modified:
		if llen != 4 && llen != 3 {
			return nil, fmt.Errorf("mismatching number of fields: expect 3..4, got: %d", llen)
		}
	default:
		if llen != 3 {
			return nil, fmt.Errorf("mismatching number of fields: expect 3, got: %d", llen)
		}
	}

	inodeType := inodeTypeMap[line[1]]
	if inodeType == 0 {
		return nil, fmt.Errorf("unknown inode type '%s'", line[1])
	}

	path, err := utils.UnescapeFilepath(line[2])
	if err != nil {
		return nil, fmt.Errorf("failed to parse filename: %w", err)
	}

	var newPath string
	var referenceCount int
	switch changeType {
	case Renamed:
		newPath, err = utils.UnescapeFilepath(line[3])
		if err != nil {
			return nil, fmt.Errorf("failed to parse filename: %w", err)
		}
	case Modified:
		if llen == 4 {
			referenceCount, err = parseReferenceCount(line[3])
			if err != nil {
				return nil, fmt.Errorf("failed to parse reference count: %w", err)
			}
		}
	default:
		newPath = ""
	}

	return &InodeChange{
		Change:               changeType,
		Type:                 inodeType,
		Path:                 path,
		NewPath:              newPath,
		ReferenceCountChange: referenceCount,
	}, nil
}

func parseInodeChanges(lines [][]string) ([]*InodeChange, error) {
	changes := make([]*InodeChange, len(lines))

	for i, line := range lines {
		c, err := parseInodeChange(line)
		if err != nil {
			return nil, fmt.Errorf("failed to parse line %d of zfs diff: %w, got: '%s'", i, err, line)
		}
		changes[i] = c
	}
	return changes, nil
}

func setString(field *string, value string) {
	v := ""
	if value != "-" {
		v = value
	}
	*field = v
}

func setBool(field *bool, value string) error {
	if value == "-" {
		*field = false
		return nil
	}

	switch strings.ToLower(value) {
	case "yes", "on":
		value = "true"
	case "no", "off":
		value = "false"
	}

	v, err := strconv.ParseBool(value)
	if err != nil {
		return err
	}

	*field = v
	return nil
}

func setUint(field *uint64, value string) error {
	var v uint64
	if value != "-" {
		var err error
		v, err = strconv.ParseUint(value, 10, 64)
		if err != nil {
			return err
		}
	}
	*field = v
	return nil
}

func propsSlice(properties map[string]string) []string {
	args := make([]string, 0, len(properties)*3)
	for k, v := range properties {
		args = append(args, "-o")
		args = append(args, fmt.Sprintf("%s=%s", k, v))
	}
	return args
}

func parseReferenceCount(field string) (int, error) {
	matches := referenceCountRegex.FindStringSubmatch(field)
	if matches == nil {
		return 0, fmt.Errorf("regexp does not match")
	}
	return strconv.Atoi(matches[1])
}

func ParseTimeUnit(value string) uint64 {
	if value == "-" {
		return 0
	}

	re := regexp.MustCompile(`([\d.]+)([a-zA-Z]*)`)
	matches := re.FindStringSubmatch(value)

	if len(matches) != 3 {
		return 0
	}

	num, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0
	}

	unit := matches[2]
	switch unit {
	case "us":
		return uint64(num)
	case "ms":
		return uint64(num * 1000)
	case "s":
		return uint64(num * 1000000)
	default:
		return uint64(num)
	}
}
