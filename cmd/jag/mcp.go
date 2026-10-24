package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jaggery-dev/jaggery/internal/home"
	"github.com/jaggery-dev/jaggery/internal/identity"
	"github.com/jaggery-dev/jaggery/internal/shares"
	"github.com/jaggery-dev/jaggery/internal/version"
)

// The house's MCP server at /mcp: shared folders on every
// machine, for models and agents. Jaggery runs every call itself, so each one
// is logged where it happens (S3) and can later be controlled per person (S5).

const mcpPath = "/mcp"

const mcpInstructions = `Files in the folders this house shares with you, on any of its machines.
Start with list_shares. Paths are relative to a share's top folder. Big files
are read in chunks: read_file says the file's size and the offset to continue
from. Only shares marked writable accept write_file and delete_file, and every
overwritten or deleted file is kept for 30 days, so it can be restored.`

// registerMCP serves /mcp on the agent's LAN port. Browsers are refused
// (cross-origin protection), so a web page can't drive the tools; agent apps
// send no Origin and get through.
func registerMCP(mux *http.ServeMux, id *identity.Identity, store *home.Store) {
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return newMCPServer(id, store, r.RemoteAddr)
	}, &mcp.StreamableHTTPOptions{SessionTimeout: 30 * time.Minute})
	mux.Handle(mcpPath, http.NewCrossOriginProtection().Handler(h))
}

type shareIn struct {
	Share string `json:"share" jsonschema:"the share's name, as list_shares shows it"`
	Path  string `json:"path,omitempty" jsonschema:"a path inside the share; empty for its top folder"`
}

type readIn struct {
	Share  string `json:"share" jsonschema:"the share's name, as list_shares shows it"`
	Path   string `json:"path" jsonschema:"the file's path inside the share"`
	Offset int64  `json:"offset,omitempty" jsonschema:"byte offset to start from, for reading a big file in chunks"`
	Length int64  `json:"length,omitempty" jsonschema:"most bytes to return (default and maximum 1 MiB)"`
}

type searchIn struct {
	Share string `json:"share" jsonschema:"the share's name, as list_shares shows it"`
	Query string `json:"query" jsonschema:"text to find in file names and in text files (not case-sensitive)"`
	Path  string `json:"path,omitempty" jsonschema:"a folder inside the share to search; empty for all of it"`
}

type writeIn struct {
	Share    string `json:"share" jsonschema:"a writable share's name"`
	Path     string `json:"path" jsonschema:"the file's path inside the share; folders are created as needed"`
	Content  string `json:"content" jsonschema:"the whole new contents of the file"`
	Encoding string `json:"encoding,omitempty" jsonschema:"text (default) or base64, for binary files"`
}

func newMCPServer(id *identity.Identity, store *home.Store, remote string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "jaggery", Title: "Jaggery shared folders", Version: version.Version},
		&mcp.ServerOptions{Instructions: mcpInstructions})
	destructive := true

	call := func(ctx context.Context, req *mcp.CallToolRequest, fr shares.Request) (shares.Response, houseShare, error) {
		who := fileCaller{From: remote}
		if p := req.Session.InitializeParams(); p != nil && p.ClientInfo != nil {
			who.App = p.ClientInfo.Name
		}
		return doFiles(ctx, id, store, fr, who)
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_shares",
		Description: "List the folders the house shares, the machine each is on, and whether it is writable.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		dir, err := identity.StateDir()
		if err != nil {
			return nil, nil, err
		}
		list := houseShares(dir, loadHouse())
		if len(list) == 0 {
			return textResult("No folders are shared yet. On the machine that holds one, run: jag share add <folder>"), nil, nil
		}
		var b strings.Builder
		for _, h := range list {
			mode := "read-only"
			if h.Writable {
				mode = "writable"
			}
			state := ""
			if !h.Online {
				state = ", offline"
			}
			fmt.Fprintf(&b, "%s: on %s, %s%s\n", h.Name, h.Machine, mode, state)
		}
		return textResult(b.String()), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_dir",
		Description: "List a folder in a share: names, sizes, types and when each changed.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in shareIn) (*mcp.CallToolResult, any, error) {
		resp, _, err := call(ctx, req, shares.Request{Op: shares.OpList, Share: in.Share, Path: in.Path})
		if err != nil {
			return nil, nil, err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s/ in %s:\n", resp.Path, in.Share)
		if len(resp.Entries) == 0 {
			b.WriteString("(empty)\n")
		}
		for _, e := range resp.Entries {
			when := e.Modified.Format("2006-01-02 15:04")
			if e.Dir {
				fmt.Fprintf(&b, "%s/  folder  %s\n", e.Name, when)
				continue
			}
			fmt.Fprintf(&b, "%s  %s  %s  %s\n", e.Name, sizeText(e.Size), mimeText(e.MIME), when)
		}
		if resp.Truncated {
			fmt.Fprintf(&b, "(only the first %d entries)\n", shares.MaxList)
		}
		return textResult(b.String()), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "read_file",
		Description: "Read a file of any type from a share. Text comes back as text, a whole image as an image, " +
			"and anything else (PDF, office documents, archives, audio) as a resource with its MIME type. " +
			"Big files are read in chunks of up to 1 MiB: the reply gives the size and the next offset.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in readIn) (*mcp.CallToolResult, any, error) {
		resp, hs, err := call(ctx, req, shares.Request{Op: shares.OpRead, Share: in.Share, Path: in.Path, Offset: in.Offset, Length: in.Length})
		if err != nil {
			return nil, nil, err
		}
		return readResult(hs, resp), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "search",
		Description: "Find files in a share by name, and lines in its text files that contain the query.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, any, error) {
		resp, _, err := call(ctx, req, shares.Request{Op: shares.OpSearch, Share: in.Share, Path: in.Path, Query: in.Query})
		if err != nil {
			return nil, nil, err
		}
		if len(resp.Matches) == 0 {
			return textResult(fmt.Sprintf("Nothing in %s matches %q.", in.Share, in.Query)), nil, nil
		}
		var b strings.Builder
		for _, m := range resp.Matches {
			if m.Line == 0 {
				fmt.Fprintf(&b, "%s (name)\n", m.Path)
			} else {
				fmt.Fprintf(&b, "%s:%d: %s\n", m.Path, m.Line, m.Text)
			}
		}
		if resp.Truncated {
			b.WriteString("(stopped at the search limit: narrow the query or the folder)\n")
		}
		return textResult(b.String()), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "write_file",
		Description: "Create or replace a whole file in a writable share. The old version is kept for 30 days. " +
			"Up to 8 MiB per write.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &destructive},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in writeIn) (*mcp.CallToolResult, any, error) {
		data := []byte(in.Content)
		switch strings.ToLower(in.Encoding) {
		case "", "text", "utf-8", "utf8":
		case "base64":
			b, err := base64.StdEncoding.DecodeString(in.Content)
			if err != nil {
				return nil, nil, fmt.Errorf("content isn't valid base64: %w", err)
			}
			data = b
		default:
			return nil, nil, fmt.Errorf("encoding %q: use text or base64", in.Encoding)
		}
		resp, hs, err := call(ctx, req, shares.Request{Op: shares.OpWrite, Share: in.Share, Path: in.Path, Data: data})
		if err != nil {
			return nil, nil, err
		}
		msg := fmt.Sprintf("Wrote %s to %s in %s.", sizeText(resp.Written), resp.Path, hs.Name)
		if resp.Version != "" {
			msg += " The previous version is kept for 30 days."
		}
		return textResult(msg), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_file",
		Description: "Delete a file (not a folder) from a writable share. It is kept for 30 days and can be restored.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &destructive},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in shareIn) (*mcp.CallToolResult, any, error) {
		resp, hs, err := call(ctx, req, shares.Request{Op: shares.OpDelete, Share: in.Share, Path: in.Path})
		if err != nil {
			return nil, nil, err
		}
		return textResult(fmt.Sprintf("Deleted %s from %s. It is kept for 30 days and can be restored.", resp.Path, hs.Name)), nil, nil
	})
	return s
}

func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// viewableImages are the image types models commonly accept.
var viewableImages = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// readResult is a line saying what was read, then the bytes in the block
// their type calls for.
func readResult(hs houseShare, resp shares.Response) *mcp.CallToolResult {
	end := resp.Offset + int64(len(resp.Data))
	whole := resp.Offset == 0 && end == resp.Size
	head := fmt.Sprintf("%s in %s: %s, %s.", resp.Path, hs.Name, mimeText(resp.MIME), sizeText(resp.Size))
	if !whole {
		head += fmt.Sprintf(" Bytes %d to %d.", resp.Offset, end)
		if end < resp.Size {
			head += fmt.Sprintf(" Continue with offset %d.", end)
		}
	}
	var body mcp.Content
	switch {
	case resp.Text:
		body = &mcp.TextContent{Text: string(resp.Data)}
	case whole && viewableImages[resp.MIME]:
		body = &mcp.ImageContent{Data: resp.Data, MIMEType: resp.MIME}
	default:
		body = &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
			URI:      "jaggery://" + url.PathEscape(hs.Name) + "/" + escapePath(resp.Path),
			MIMEType: resp.MIME,
			Blob:     resp.Data,
		}}
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: head}, body}}
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

func sizeText(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d bytes", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
}

func mimeText(mime string) string {
	if mime == "" {
		return "unknown type"
	}
	return mime
}
