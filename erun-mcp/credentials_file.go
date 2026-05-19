package erunmcp

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// awsCredentialEntry is one key=value line inside an AWS credentials profile.
type awsCredentialEntry struct {
	Key   string
	Value string
}

// awsCredentialProfile is one [name] section of ~/.aws/credentials.
type awsCredentialProfile struct {
	Name    string
	Entries []awsCredentialEntry
}

func readAWSCredentialsFile(path string) ([]awsCredentialProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return parseAWSCredentialsFile(data), nil
}

func parseAWSCredentialsFile(data []byte) []awsCredentialProfile {
	var profiles []awsCredentialProfile
	var current *awsCredentialProfile
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.TrimSpace(line[1 : len(line)-1])
			if name == "" {
				current = nil
				continue
			}
			profiles = append(profiles, awsCredentialProfile{Name: name})
			current = &profiles[len(profiles)-1]
			continue
		}
		if current == nil {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		value := strings.TrimSpace(line[eq+1:])
		if key == "" {
			continue
		}
		current.Entries = append(current.Entries, awsCredentialEntry{Key: key, Value: value})
	}
	return profiles
}

func setAWSCredentialProfile(profiles []awsCredentialProfile, name string, entries []awsCredentialEntry) []awsCredentialProfile {
	name = strings.TrimSpace(name)
	for i, profile := range profiles {
		if profile.Name == name {
			profiles[i].Entries = entries
			return profiles
		}
	}
	return append(profiles, awsCredentialProfile{Name: name, Entries: entries})
}

func removeAWSCredentialProfile(profiles []awsCredentialProfile, name string) ([]awsCredentialProfile, bool) {
	name = strings.TrimSpace(name)
	for i, profile := range profiles {
		if profile.Name == name {
			return append(profiles[:i:i], profiles[i+1:]...), true
		}
	}
	return profiles, false
}

func serializeAWSCredentialsFile(profiles []awsCredentialProfile) []byte {
	var buf bytes.Buffer
	for i, profile := range profiles {
		if i > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteByte('[')
		buf.WriteString(profile.Name)
		buf.WriteString("]\n")
		for _, entry := range profile.Entries {
			buf.WriteString(entry.Key)
			buf.WriteString(" = ")
			buf.WriteString(entry.Value)
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes()
}

func writeAWSCredentialsFile(path string, profiles []awsCredentialProfile) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".credentials-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = os.Remove(tmpPath)
	}
	if _, err := tmp.Write(serializeAWSCredentialsFile(profiles)); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("rename %s: %w", filepath.Base(tmpPath), err)
	}
	return nil
}
