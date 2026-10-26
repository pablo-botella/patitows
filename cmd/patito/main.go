// Command patito controls running patitows instances on the same machine.
//
//	patito -i <instance> -updateblob -blobid <id> -blobfile <file> [-restart]
//	patito -i <instance> -stop
//
// -updateblob checks the new blob and writes it into the instance's XML
// configuration; with -restart the instance also swaps it hot, without
// dropping connections. -stop shuts the instance down gracefully.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pablo-botella/mskblob"
	"github.com/pablo-botella/patitows"
)

func main() {
	instance := flag.String("i", "", "instance id (required)")
	updateBlob := flag.Bool("updateblob", false, "point a blob resource to a new file")
	blobID := flag.String("blobid", "", "blob resource id (with -updateblob)")
	blobFile := flag.String("blobfile", "", "new blob file (with -updateblob)")
	restart := flag.Bool("restart", false, "with -updateblob: swap the blob in the running instance")
	stop := flag.Bool("stop", false, "stop the instance gracefully")
	flag.Parse()

	if flag.NArg() > 0 {
		fail("unexpected arguments: %s", strings.Join(flag.Args(), " "))
	}
	if *instance == "" {
		fail("-i is required")
	}
	switch {
	case *updateBlob && *stop:
		fail("use only one of -updateblob and -stop")
	case *updateBlob:
		doUpdateBlob(*instance, *blobID, *blobFile, *restart)
	case *stop:
		resp := send(*instance, patitows.Request{Cmd: patitows.CmdStop})
		fmt.Printf("instance %q stopped\n", *instance)
		_ = resp
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func doUpdateBlob(instance, id, file string, restart bool) {
	if id == "" || file == "" {
		fail("-updateblob needs -blobid and -blobfile")
	}
	// The value is written into the XML as given; a relative path is made
	// absolute first, since the XML resolves relative paths against its own
	// directory.
	value := file
	if !strings.HasPrefix(file, "~") && !filepath.IsAbs(file) {
		abs, err := filepath.Abs(file)
		if err != nil {
			fail("%v", err)
		}
		value = abs
	}
	b, err := mskblob.Open(patitows.ExpandHome(value))
	if err != nil {
		fail("blob %s: %v", value, err)
	}
	b.Close()

	info := send(instance, patitows.Request{Cmd: patitows.CmdInfo})
	if err := patitows.SetBlobFile(info.Config, id, value); err != nil {
		fail("%v", err)
	}
	fmt.Printf("%s: blob %q -> %s\n", info.Config, id, value)
	if !restart {
		return
	}
	resp := send(instance, patitows.Request{Cmd: patitows.CmdReloadBlob, BlobID: id})
	fmt.Printf("instance %q now serves %s\n", instance, resp.BlobFile)
}

func send(instance string, req patitows.Request) *patitows.Response {
	resp, err := patitows.Send(instance, req)
	if err != nil {
		fail("%v", err)
	}
	if !resp.OK {
		fail("instance %q: %s", instance, resp.Error)
	}
	return resp
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "patito: "+format+"\n", args...)
	os.Exit(1)
}
