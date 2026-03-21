// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// Config represents a parsed and loaded Reticulum network configuration containing a hierarchy of sections and properties.
type Config struct {
	Sections map[string]*ConfigSection
}

// ConfigSection represents an isolated subset of configuration directives within a Reticulum configuration.
type ConfigSection struct {
	Name        string
	Properties  map[string]string
	Subsections map[string]*ConfigSection
}

// NewConfig initializes an empty configuration structure ready for programmatic population or parsing.
func NewConfig() *Config {
	return &Config{
		Sections: make(map[string]*ConfigSection),
	}
}

// LoadConfig reads and parses a Reticulum configuration directly from a file path on the local filesystem.
func LoadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := f.Close(); err != nil {
			Logf("Warning: Could not close config file %v properly: %v", LogWarning, false, path, err)
		}
	}()
	return ParseConfig(f)
}

// ParseConfig consumes an input stream to parse and validate a Reticulum INI-style configuration file structure.
func ParseConfig(r io.Reader) (*Config, error) {
	config := NewConfig()
	scanner := bufio.NewScanner(r)

	var currentSection *ConfigSection
	var currentSubsection *ConfigSection
	var currentNestedSubsection *ConfigSection

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		if strings.HasPrefix(line, "[[[") && strings.HasSuffix(line, "]]]") {
			name := strings.Trim(line, "[] ")
			if currentSubsection == nil {
				return nil, fmt.Errorf("nested subsection %v found outside of subsection", name)
			}
			currentNestedSubsection = &ConfigSection{
				Name:        name,
				Properties:  make(map[string]string),
				Subsections: make(map[string]*ConfigSection),
			}
			currentSubsection.Subsections[name] = currentNestedSubsection
		} else if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") {
			// Subsection
			name := strings.Trim(line, "[] ")
			if currentSection == nil {
				return nil, fmt.Errorf("subsection %v found outside of section", name)
			}
			currentSubsection = &ConfigSection{
				Name:        name,
				Properties:  make(map[string]string),
				Subsections: make(map[string]*ConfigSection),
			}
			currentSection.Subsections[name] = currentSubsection
			currentNestedSubsection = nil
		} else if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			// Section
			name := strings.Trim(line, "[] ")
			currentSection = &ConfigSection{
				Name:        name,
				Properties:  make(map[string]string),
				Subsections: make(map[string]*ConfigSection),
			}
			config.Sections[name] = currentSection
			currentSubsection = nil
			currentNestedSubsection = nil
		} else if strings.Contains(line, "=") {
			// Property
			parts := strings.SplitN(line, "=", 2)
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])

			if currentNestedSubsection != nil {
				currentNestedSubsection.Properties[key] = value
			} else if currentSubsection != nil {
				currentSubsection.Properties[key] = value
			} else if currentSection != nil {
				currentSection.Properties[key] = value
			}
		}
	}

	return config, scanner.Err()
}

// GetSection retrieves an entire top-level configuration section by its designated name.
func (c *Config) GetSection(name string) (*ConfigSection, bool) {
	s, ok := c.Sections[name]
	return s, ok
}

// GetProperty retrieves a specific string value mapped to a given key within this configuration section.
func (s *ConfigSection) GetProperty(key string) (string, bool) {
	v, ok := s.Properties[key]
	return v, ok
}
