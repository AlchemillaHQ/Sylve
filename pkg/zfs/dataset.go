package zfs

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

type Dataset struct {
	z             *zfs   `json:"-"`
	Name          string `json:"name"`
	GUID          string `json:"guid"`
	Origin        string `json:"origin"`
	Used          uint64 `json:"used"`
	Avail         uint64 `json:"avail"`
	Recordsize    uint64 `json:"recordsize"`
	Mountpoint    string `json:"mountpoint"`
	Compression   string `json:"compression"`
	Type          string `json:"type"`
	Written       uint64 `json:"written"`
	Volsize       uint64 `json:"volsize"`
	VolBlockSize  uint64 `json:"volblocksize"`
	Logicalused   uint64 `json:"logicalused"`
	Usedbydataset uint64 `json:"usedbydataset"`
	Quota         uint64 `json:"quota"`
	Referenced    uint64 `json:"referenced"`
	Mounted       string `json:"mounted"`
	Checksum      string `json:"checksum"`
	Dedup         string `json:"dedup"`
	ACLInherit    string `json:"aclinherit"`
	ACLMode       string `json:"aclmode"`
	PrimaryCache  string `json:"primarycache"`
	VolMode       string `json:"volmode"`

	Props map[string]string `json:"properties"`
}

func (d *Dataset) Clone(dest string, properties map[string]string) (*Dataset, error) {
	if d.Type != DatasetSnapshot {
		return nil, errors.New("can only clone snapshots")
	}
	args := make([]string, 2, 4)
	args[0] = "clone"
	args[1] = "-p"
	if properties != nil {
		args = append(args, propsSlice(properties)...)
	}
	args = append(args, []string{d.Name, dest}...)
	if err := d.z.do(args...); err != nil {
		return nil, err
	}
	return d.z.GetDataset(dest)
}

func (d *Dataset) Unmount(force bool) (*Dataset, error) {
	if d.Type == DatasetSnapshot {
		return nil, errors.New("cannot unmount snapshots")
	}
	args := make([]string, 1, 3)
	args[0] = "umount"
	if force {
		args = append(args, "-f")
	}
	args = append(args, d.Name)
	if err := d.z.do(args...); err != nil {
		return nil, err
	}
	return d.z.GetDataset(d.Name)
}

func (d *Dataset) Mount(overlay bool, options []string) (*Dataset, error) {
	if d.Type == DatasetSnapshot {
		return nil, errors.New("cannot mount snapshots")
	}
	args := make([]string, 1, 5)
	args[0] = "mount"
	if overlay {
		args = append(args, "-O")
	}
	if options != nil {
		args = append(args, "-o")
		args = append(args, strings.Join(options, ","))
	}
	args = append(args, d.Name)
	if err := d.z.do(args...); err != nil {
		return nil, err
	}
	return d.z.GetDataset(d.Name)
}

func (d *Dataset) Destroy(flags DestroyFlag) error {
	args := make([]string, 1, 3)
	args[0] = "destroy"
	if flags&DestroyRecursive != 0 {
		args = append(args, "-r")
	}

	if flags&DestroyRecursiveClones != 0 {
		args = append(args, "-R")
	}

	if flags&DestroyDeferDeletion != 0 {
		args = append(args, "-d")
	}

	if flags&DestroyForceUmount != 0 {
		args = append(args, "-f")
	}

	args = append(args, d.Name)
	err := d.z.do(args...)
	return err
}

func (d *Dataset) SetProperty(key, val string) error {
	prop := strings.Join([]string{key, val}, "=")
	if err := d.z.do("set", prop, d.Name); err != nil {
		return err
	}
	d.Props[strings.ToLower(key)] = val
	return nil
}

func (d *Dataset) SetProperties(keyValPairs ...string) error {
	if len(keyValPairs) == 0 {
		return nil
	}
	if len(keyValPairs)%2 != 0 {
		return errors.New("keyValPairs must be an even number of strings")
	}
	args := []string{"set"}
	props := make(map[string]string)
	for i := 0; i < len(keyValPairs); i += 2 {
		props[strings.ToLower(keyValPairs[i])] = keyValPairs[i+1]
		args = append(args, strings.Join(keyValPairs[i:i+2], "="))
	}
	args = append(args, d.Name)
	if err := d.z.do(args...); err != nil {
		return err
	}
	for k, v := range props {
		d.Props[k] = v
	}
	return nil
}

func (d *Dataset) GetProperty(key string) (string, error) {
	if v, ok := d.Props[strings.ToLower(key)]; ok {
		return v, nil
	}
	// custom properties does not return error
	if strings.Contains(key, ":") {
		return "-", nil
	}
	out, err := d.z.doOutput("get", "-H", "-p", key, d.Name)
	if err != nil {
		return "", err
	}

	return out[0][2], nil
}

func (d *Dataset) GetProperties(keys ...string) ([]string, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	props, failed := make([]string, 0, len(keys)), false
	for _, v := range keys {
		val, ok := d.Props[strings.ToLower(v)]
		if failed = !ok && !strings.Contains(v, ":"); failed {
			props = make([]string, 0, len(keys))
			break
		}
		if val == "" {
			val = "-"
		}
		props = append(props, val)
	}
	if !failed {
		return props, nil
	}
	out, err := d.z.doOutput("get", "-H", "-p", strings.Join(keys, ","), d.Name)
	if err != nil {
		return nil, err
	}
	for _, v := range out {
		props = append(props, v[2])
	}
	return props, nil
}

func (d *Dataset) GetAllProperties() (map[string]string, error) {
	out, err := d.z.doOutput("get", "-H", "-p", "all", d.Name)
	if err != nil {
		return nil, err
	}
	props := make(map[string]string)
	for _, v := range out {
		props[v[1]] = v[2]
	}
	return props, nil
}

func (d *Dataset) Rename(name string, force, createParent, recursiveRenameSnapshots bool) (*Dataset, error) {
	args := []string{"rename"}

	if createParent {
		args = append(args, "-p")
	}

	if force {
		args = append(args, "-f")
	}

	if recursiveRenameSnapshots {
		args = append(args, "-r")
	}

	args = append(args, d.Name, name)

	if err := d.z.do(args...); err != nil {
		return d, err
	}

	return d.z.GetDataset(name)
}

func (d *Dataset) Snapshots() ([]*Dataset, error) {
	return d.z.Snapshots(d.Name)
}

func (d *Dataset) SendSnapshot(output io.Writer) error {
	if d.Type != DatasetSnapshot {
		return errors.New("can only send snapshots")
	}
	_, err := d.z.run(nil, output, "zfs", "send", d.Name)
	return err
}

func (d *Dataset) SendSnapshotToDataset(destDataset *Dataset, force bool) error {
	if d == nil || destDataset == nil {
		return errors.New("nil dataset")
	}
	if d.Type != DatasetSnapshot {
		return errors.New("can only send snapshots")
	}

	pr, pw := io.Pipe()
	var wg sync.WaitGroup
	var sendErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		// If SendSnapshot fails, CloseWithError signals the reader side.
		if err := d.SendSnapshot(pw); err != nil {
			_ = pw.CloseWithError(err)
			sendErr = err
			return
		}
		_ = pw.Close()
	}()

	// Run recv; out=nil so run captures stdout internally, and error (if any) will include stderr.
	// _, recvErr := d.z.run(pr, nil, "zfs", "recv", destDataset.Name)
	var recvErr error
	recvArgs := []string{"recv"}
	if force {
		recvArgs = append(recvArgs, "-F")
	}

	recvArgs = append(recvArgs, destDataset.Name)
	_, recvErr = d.z.run(pr, nil, "zfs", recvArgs...)

	// close read end and wait for sender to finish (synchronizes sendErr)
	_ = pr.Close()
	wg.Wait()

	if sendErr != nil {
		return fmt.Errorf("send failed: %w", sendErr)
	}
	if recvErr != nil {
		// If your run returns *Error, include its stderr for debugging.
		if e, ok := recvErr.(*Error); ok {
			return fmt.Errorf("recv failed: %v: %s", e.Err, e.Stderr)
		}
		return fmt.Errorf("recv failed: %w", recvErr)
	}
	return nil
}

func (d *Dataset) IncrementalSend(baseSnapshot *Dataset, output io.Writer) error {
	if d.Type != DatasetSnapshot || baseSnapshot.Type != DatasetSnapshot {
		return errors.New("can only send snapshots")
	}
	_, err := d.z.run(nil, output, "zfs", "send", "-i", baseSnapshot.Name, d.Name)
	return err
}

func (d *Dataset) Snapshot(name string, recursive bool) (*Dataset, error) {
	args := make([]string, 1, 4)
	args[0] = "snapshot"
	if recursive {
		args = append(args, "-r")
	}
	snapName := fmt.Sprintf("%s@%s", d.Name, name)
	args = append(args, snapName)
	if err := d.z.do(args...); err != nil {
		return nil, err
	}
	return d.z.GetDataset(snapName)
}

func (d *Dataset) Rollback(destroyMoreRecent bool) error {
	if d.Type != DatasetSnapshot {
		return errors.New("can only rollback snapshots")
	}

	args := make([]string, 1, 3)
	args[0] = "rollback"
	if destroyMoreRecent {
		args = append(args, "-r")
	}
	args = append(args, d.Name)

	err := d.z.do(args...)
	return err
}

func (d *Dataset) Children(depth uint64) ([]*Dataset, error) {
	args := []string{"list"}
	if depth > 0 {
		args = append(args, "-d")
		args = append(args, strconv.FormatUint(depth, 10))
	} else {
		args = append(args, "-r")
	}
	args = append(args, "-t", "all", "-p", "-o", "all")
	args = append(args, d.Name)

	out, err := d.z.doOutput(args...)
	if err != nil {
		return nil, err
	}

	if len(out) == 0 {
		return nil, nil
	}

	var datasets []*Dataset
	name := ""
	var ds *Dataset
	for _, line := range out[1:] {
		if name != line[0] {
			name = line[0]
			ds = &Dataset{z: d.z, Name: name, Props: make(map[string]string)}
			datasets = append(datasets, ds)
		}
		if err := ds.parseProps([][]string{out[0], line}); err != nil {
			return nil, err
		}
	}
	return datasets[1:], nil
}

func (d *Dataset) Diff(snapshot string) ([]*InodeChange, error) {
	args := []string{"diff", "-FH", snapshot, d.Name}
	out, err := d.z.doOutput(args...)
	if err != nil {
		return nil, err
	}
	inodeChanges, err := parseInodeChanges(out)
	if err != nil {
		return nil, err
	}
	return inodeChanges, nil
}
