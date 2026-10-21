package install

import (
	"strings"
	"testing"
)

func TestDesktopEntryOpensLinks(t *testing.T) {
	l := layoutFor("linux", "/home/p", func(string) string { return "" })
	entry := entryText(l)
	// The link comes as the one argument; from the menu, none.
	if !strings.Contains(entry, "\nExec=\"/home/p/.local/bin/axon\" %u\n") {
		t.Errorf("Exec does not take the link:\n%s", entry)
	}
	if !strings.Contains(entry, "\nMimeType=x-scheme-handler/"+URLScheme+";\n") {
		t.Errorf("not the handler for %s:// links:\n%s", URLScheme, entry)
	}
}
