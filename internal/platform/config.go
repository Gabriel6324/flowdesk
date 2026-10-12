package platform

import (
	"flowdesk/internal/domain"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type Config struct {
	Profile        string
	DataDir        string
	Listen         string
	PublicOrigin   string
	LiveEnabled    bool
	EgressHosts    []string
	TrustedProxies []*net.IPNet
	NoBrowser      bool
	Workspace      bool
}

func (c Config) WorkspaceMode() string {
	if c.Profile == "server" {
		return "server"
	}
	if c.Workspace {
		return "personal"
	}
	return "demo"
}

func DefaultDir() string {
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "FlowDesk")
		}
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" && filepath.IsAbs(d) {
		return filepath.Join(d, "FlowDesk")
	}
	d, _ := os.UserHomeDir()
	return filepath.Join(d, ".local", "share", "FlowDesk")
}
func (c *Config) Validate(serving bool) error {
	if c.Profile == "" {
		c.Profile = "desktop"
	}
	if !domain.Enum(c.Profile, "desktop", "server") {
		return fmt.Errorf("SERVER_CONFIG_INVALID: profile")
	}
	if c.Workspace && c.Profile != "desktop" {
		return fmt.Errorf("SERVER_CONFIG_INVALID: workspace requires desktop")
	}
	if c.DataDir == "" {
		c.DataDir = DefaultDir()
		if c.Workspace {
			c.DataDir = filepath.Join(c.DataDir, "workspace")
		}
	}
	d, e := filepath.Abs(c.DataDir)
	if e != nil {
		return e
	}
	c.DataDir = d
	if c.Listen == "" {
		if c.Profile == "server" {
			c.Listen = "0.0.0.0:8787"
		} else {
			c.Listen = "127.0.0.1:8787"
		}
	}
	h, p, e := net.SplitHostPort(c.Listen)
	if e != nil {
		return fmt.Errorf("SERVER_CONFIG_INVALID: listen")
	}
	n, e := strconv.Atoi(p)
	if e != nil || n < 0 || n > 65535 {
		return fmt.Errorf("SERVER_CONFIG_INVALID: port")
	}
	if c.Profile == "desktop" && h != "127.0.0.1" {
		return fmt.Errorf("SERVER_CONFIG_INVALID: desktop requires 127.0.0.1")
	}
	if c.Profile == "server" {
		c.NoBrowser = true
		if serving {
			u, e := url.Parse(c.PublicOrigin)
			if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
				return fmt.Errorf("SERVER_CONFIG_INVALID: public origin")
			}
			c.PublicOrigin = strings.TrimSuffix(c.PublicOrigin, "/")
			if c.LiveEnabled && len(c.EgressHosts) == 0 {
				return fmt.Errorf("SERVER_CONFIG_INVALID: egress hosts")
			}
		}
	}
	for _, p := range c.TrustedProxies {
		ones, _ := p.Mask.Size()
		if ones == 0 {
			return fmt.Errorf("SERVER_CONFIG_INVALID: unrestricted trusted proxy")
		}
	}
	return nil
}
func ParseCIDRs(s string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, v := range strings.Split(s, ",") {
		if strings.TrimSpace(v) == "" {
			continue
		}
		_, p, e := net.ParseCIDR(strings.TrimSpace(v))
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, nil
}
