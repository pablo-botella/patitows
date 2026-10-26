package patitows

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pablo-botella/cargoxml"
)

// SetBlobFile rewrites the file attribute of the <blob> with the given id
// in the configuration file at path. Everything else in the document —
// comments, formatting, unknown content — is preserved. The file is
// replaced atomically.
func SetBlobFile(path, blobID, file string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	d := cargoxml.NewDecoderWithCargo(xml.NewDecoder(f))
	err = d.Parse()
	f.Close()
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	root, ok := d.RootFrame.Consumer.(*cargoxml.GenericXmlItem)
	if !ok {
		return fmt.Errorf("%s: unexpected root", path)
	}

	found := 0
	var walk func(n *cargoxml.GenericXmlItem)
	walk = func(n *cargoxml.GenericXmlItem) {
		if n.Name.Local == "blob" && attr(n, "id") == blobID {
			setAttr(n, "file", file)
			found++
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	if found != 1 {
		return fmt.Errorf("%s: blob id %q found %d times", path, blobID, found)
	}

	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	enc := xml.NewEncoder(tmp)
	if err := cargoxml.NewEncoderWithCargo(enc).Encode(root); err != nil {
		tmp.Close()
		return err
	}
	if err := enc.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func attr(n *cargoxml.GenericXmlItem, name string) string {
	for _, a := range n.Attributes {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func setAttr(n *cargoxml.GenericXmlItem, name, value string) {
	for i := range n.Attributes {
		if n.Attributes[i].Name.Local == name {
			n.Attributes[i].Value = value
			return
		}
	}
	n.Attributes = append(n.Attributes, xml.Attr{Name: xml.Name{Local: name}, Value: value})
}
