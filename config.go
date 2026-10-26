package patitows

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pablo-botella/cargoxml"
)

// Config is the whole patitows configuration, read from an XML file:
//
//	<patitows id="main">
//	  <acme cache="certs" email="admin@example.com" staging="true"/>
//	  <listen https=":443" http=""/>
//	  <site name="example">
//	    <host name="example.com"/>
//	    <host name="www.example.com"/>
//	    <blob id="example-web" file="~/sites/example-20261026180500.blob" base="/"/>
//	  </site>
//	</patitows>
//
// The instance id, <acme> with cache and <listen> with https and http are
// mandatory: there are no defaults. An empty http disables the
// HTTP-to-HTTPS redirect. A site has zero or more blobs; every blob needs
// an id unique in the whole file, a file and a base path. Unknown
// attributes and elements are ignored.
type Config struct {
	cargoxml.NullXmlConsumer
	ID     string // instance id, unique on the system
	Path   string // absolute path of the configuration file
	ACME   ACMEConfig
	Listen ListenConfig
	Sites  []*Site
}

// ACMEConfig holds the certificate settings (TLS-ALPN-01 via autocert).
type ACMEConfig struct {
	cargoxml.NullXmlConsumer
	Cache    string // certificates and account key; relative to the config file
	Email    string
	Staging  bool
	err      error
	seen     bool
	hasCache bool
}

// ListenConfig holds the listen addresses. An empty HTTP disables the
// HTTP-to-HTTPS redirect listener.
type ListenConfig struct {
	cargoxml.NullXmlConsumer
	HTTPS    string
	HTTP     string
	seen     bool
	hasHTTPS bool
	hasHTTP  bool
}

// Site is one site answering for one or more host names.
type Site struct {
	cargoxml.NullXmlConsumer
	Name  string
	Hosts []string
	Blobs []*BlobConfig
}

// BlobConfig is a <blob> resource: an mskblob file mounted under Base.
type BlobConfig struct {
	cargoxml.NullXmlConsumer
	ID   string
	File string // as written in the configuration
	Base string // URL path prefix, starting and ending with "/"
	Path string // File resolved: "~" expanded, relative to the config file
}

// siteHost is the consumer of a <host> element inside a <site>.
type siteHost struct {
	cargoxml.NullXmlConsumer
	Name string
}

// LoadConfig reads and validates the configuration file at path.
func LoadConfig(path string) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	cfg := &Config{Path: abs}
	d := cargoxml.NewDecoderWithCargo(xml.NewDecoder(f))
	d.Root = cfg
	if err := d.Parse(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	dir := filepath.Dir(abs)
	cfg.ACME.Cache = resolvePath(cfg.ACME.Cache, dir)
	for _, s := range cfg.Sites {
		for _, b := range s.Blobs {
			b.Path = resolvePath(b.File, dir)
		}
	}
	return cfg, nil
}

// Hosts returns every host name of every site.
func (c *Config) Hosts() []string {
	var hosts []string
	for _, s := range c.Sites {
		hosts = append(hosts, s.Hosts...)
	}
	return hosts
}

// Blob returns the blob resource with the given id, or nil.
func (c *Config) Blob(id string) *BlobConfig {
	for _, s := range c.Sites {
		for _, b := range s.Blobs {
			if b.ID == id {
				return b
			}
		}
	}
	return nil
}

// ExpandHome replaces a leading "~" with the current user's home directory.
func ExpandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[1:])
}

// resolvePath expands "~" and makes a relative path relative to dir.
func resolvePath(p, dir string) string {
	p = ExpandHome(p)
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return p
}

func (c *Config) validate() error {
	if c.ID == "" {
		return errors.New("<patitows> id is required")
	}
	if !ValidInstanceID(c.ID) {
		return fmt.Errorf("invalid instance id %q (use letters, digits, '-', '_' and '.')", c.ID)
	}
	if !c.ACME.seen {
		return errors.New("missing <acme>")
	}
	if c.ACME.err != nil {
		return c.ACME.err
	}
	if !c.ACME.hasCache || c.ACME.Cache == "" {
		return errors.New("acme: cache is required")
	}
	if !c.Listen.seen {
		return errors.New("missing <listen>")
	}
	if !c.Listen.hasHTTPS || c.Listen.HTTPS == "" {
		return errors.New("listen: https address is required")
	}
	if !c.Listen.hasHTTP {
		return errors.New(`listen: http is required (use http="" to disable it)`)
	}
	if len(c.Sites) == 0 {
		return errors.New("no sites defined")
	}
	names := map[string]bool{}
	hosts := map[string]string{}
	ids := map[string]bool{}
	for _, s := range c.Sites {
		if s.Name == "" {
			return errors.New("site without name")
		}
		if names[s.Name] {
			return fmt.Errorf("site %q defined twice", s.Name)
		}
		names[s.Name] = true
		if len(s.Hosts) == 0 {
			return fmt.Errorf("site %q has no hosts", s.Name)
		}
		for _, h := range s.Hosts {
			if other, ok := hosts[h]; ok {
				return fmt.Errorf("host %q used by sites %q and %q", h, other, s.Name)
			}
			hosts[h] = s.Name
		}
		bases := map[string]bool{}
		for _, b := range s.Blobs {
			if b.ID == "" {
				return fmt.Errorf("site %q: blob without id", s.Name)
			}
			if ids[b.ID] {
				return fmt.Errorf("blob id %q defined twice", b.ID)
			}
			ids[b.ID] = true
			if b.File == "" {
				return fmt.Errorf("blob %q: file is required", b.ID)
			}
			if !strings.HasPrefix(b.Base, "/") || !strings.HasSuffix(b.Base, "/") {
				return fmt.Errorf("blob %q: base must start and end with '/'", b.ID)
			}
			if bases[b.Base] {
				return fmt.Errorf("site %q: base %q used by two blobs", s.Name, b.Base)
			}
			bases[b.Base] = true
		}
	}
	return nil
}

// ValidInstanceID reports whether id is usable as an instance id: letters,
// digits, '-', '_' and '.'.
func ValidInstanceID(id string) bool {
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return id != ""
}

func (c *Config) OnXmlStart(d *cargoxml.DecoderWithCargo, frame *cargoxml.DecoderStackFrame) error {
	if frame.NodeName.Local != "patitows" {
		return fmt.Errorf("root element is <%s>, expected <patitows>", frame.NodeName.Local)
	}
	frame.SkipUnknownChildren = true
	return nil
}

func (c *Config) OnXmlAttribute(d *cargoxml.DecoderWithCargo, attr *xml.Attr) bool {
	if attr.Name.Local == "id" {
		c.ID = attr.Value
		return true
	}
	return false
}

func (c *Config) OnXmlChildStart(d *cargoxml.DecoderWithCargo, child *cargoxml.DecoderStackFrame) error {
	switch child.NodeName.Local {
	case "acme":
		if c.ACME.seen {
			return errors.New("<acme> defined twice")
		}
		c.ACME.seen = true
		child.Consumer = &c.ACME
	case "listen":
		if c.Listen.seen {
			return errors.New("<listen> defined twice")
		}
		c.Listen.seen = true
		child.Consumer = &c.Listen
	case "site":
		s := &Site{}
		c.Sites = append(c.Sites, s)
		child.Consumer = s
	}
	return nil
}

func (a *ACMEConfig) OnXmlAttribute(d *cargoxml.DecoderWithCargo, attr *xml.Attr) bool {
	switch attr.Name.Local {
	case "cache":
		a.Cache = attr.Value
		a.hasCache = true
	case "email":
		a.Email = attr.Value
	case "staging":
		b, err := strconv.ParseBool(attr.Value)
		if err != nil {
			a.err = fmt.Errorf("acme: invalid staging value %q", attr.Value)
		}
		a.Staging = b
	default:
		return false
	}
	return true
}

func (l *ListenConfig) OnXmlAttribute(d *cargoxml.DecoderWithCargo, attr *xml.Attr) bool {
	switch attr.Name.Local {
	case "https":
		l.HTTPS = attr.Value
		l.hasHTTPS = true
	case "http":
		l.HTTP = attr.Value
		l.hasHTTP = true
	default:
		return false
	}
	return true
}

func (s *Site) OnXmlAttribute(d *cargoxml.DecoderWithCargo, attr *xml.Attr) bool {
	if attr.Name.Local == "name" {
		s.Name = attr.Value
		return true
	}
	return false
}

func (s *Site) OnXmlChildStart(d *cargoxml.DecoderWithCargo, child *cargoxml.DecoderStackFrame) error {
	switch child.NodeName.Local {
	case "host":
		child.Consumer = &siteHost{}
	case "blob":
		b := &BlobConfig{}
		s.Blobs = append(s.Blobs, b)
		child.Consumer = b
	}
	return nil
}

func (s *Site) OnXmlChildEnd(d *cargoxml.DecoderWithCargo, child *cargoxml.DecoderStackFrame) error {
	if h, ok := child.Consumer.(*siteHost); ok {
		if name := strings.ToLower(strings.TrimSpace(h.Name)); name != "" {
			s.Hosts = append(s.Hosts, name)
		}
	}
	return nil
}

func (h *siteHost) OnXmlAttribute(d *cargoxml.DecoderWithCargo, attr *xml.Attr) bool {
	if attr.Name.Local == "name" {
		h.Name = attr.Value
		return true
	}
	return false
}

func (b *BlobConfig) OnXmlAttribute(d *cargoxml.DecoderWithCargo, attr *xml.Attr) bool {
	switch attr.Name.Local {
	case "id":
		b.ID = attr.Value
	case "file":
		b.File = attr.Value
	case "base":
		b.Base = attr.Value
	default:
		return false
	}
	return true
}
