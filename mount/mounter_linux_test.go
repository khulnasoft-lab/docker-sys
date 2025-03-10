package mount

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/khulnasoft-lab/docker-sys/mountinfo"
)

func TestMount(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("root required")
	}

	source := t.TempDir()
	// Ensure we have a known start point by mounting tmpfs with given options
	if err := Mount("tmpfs", source, "tmpfs", "private"); err != nil {
		t.Fatal(err)
	}
	defer ensureUnmount(t, source)
	validateMount(t, source, "", "", "")
	if t.Failed() {
		t.FailNow()
	}

	target := t.TempDir()

	tests := []struct {
		source           string
		ftype            string
		options          string
		expectedOpts     string
		expectedOptional string
		expectedVFS      string
	}{
		// No options
		{"tmpfs", "tmpfs", "", "", "", ""},
		// tmpfs mount with noexec set
		{"tmpfs", "tmpfs", "noexec", "noexec", "", ""},
		// Default rw / ro test
		{source, "", "bind", "", "", ""},
		{source, "", "bind,private", "", "", ""},
		{source, "", "bind,shared", "", "shared", ""},
		{source, "", "bind,slave", "", "master", ""},
		{source, "", "bind,unbindable", "", "unbindable", ""},
		// Read Write tests
		{source, "", "bind,rw", "rw", "", ""},
		{source, "", "bind,rw,private", "rw", "", ""},
		{source, "", "bind,rw,shared", "rw", "shared", ""},
		{source, "", "bind,rw,slave", "rw", "master", ""},
		{source, "", "bind,rw,unbindable", "rw", "unbindable", ""},
		// Read Only tests
		{source, "", "bind,ro", "ro", "", ""},
		{source, "", "bind,ro,private", "ro", "", ""},
		{source, "", "bind,ro,shared", "ro", "shared", ""},
		{source, "", "bind,ro,slave", "ro", "master", ""},
		{source, "", "bind,ro,unbindable", "ro", "unbindable", ""},
		// Remount tests to change per filesystem options
		{"", "", "remount,size=128k", "rw", "", "rw,size=128k"},
		{"", "", "remount,ro,size=128k", "ro", "", "ro,size=128k"},
	}

	for _, tc := range tests {
		ftype, options := tc.ftype, tc.options
		if tc.ftype == "" {
			ftype = "none"
		}
		if tc.options == "" {
			options = "none"
		}

		t.Run(fmt.Sprintf("%v-%v", ftype, options), func(t *testing.T) {
			if strings.Contains(tc.options, "slave") {
				// Slave requires a shared source
				if err := MakeShared(source); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := MakePrivate(source); err != nil {
						t.Fatal(err)
					}
				}()
			}
			if strings.Contains(tc.options, "remount") {
				// create a new mount to remount first
				if err := Mount("tmpfs", target, "tmpfs", ""); err != nil {
					t.Fatal(err)
				}
			}
			if err := Mount(tc.source, target, tc.ftype, tc.options); err != nil {
				t.Fatal(err)
			}
			defer ensureUnmount(t, target)
			validateMount(t, target, tc.expectedOpts, tc.expectedOptional, tc.expectedVFS)
		})
	}
}

// ensureUnmount umounts mnt checking for errors
func ensureUnmount(t *testing.T, mnt string) {
	if err := Unmount(mnt); err != nil {
		t.Error(err)
	}
}

// validateMount checks that mnt has the given options
func validateMount(t *testing.T, mnt string, opts, optional, vfs string) {
	info, err := mountinfo.GetMounts(nil)
	if err != nil {
		t.Fatal(err)
	}

	wantedOpts := make(map[string]struct{})
	if opts != "" {
		for _, opt := range strings.Split(opts, ",") {
			wantedOpts[opt] = struct{}{}
		}
	}

	wantedOptional := make(map[string]struct{})
	if optional != "" {
		for _, opt := range strings.Split(optional, ",") {
			wantedOptional[opt] = struct{}{}
		}
	}

	wantedVFS := make(map[string]struct{})
	if vfs != "" {
		for _, opt := range strings.Split(vfs, ",") {
			wantedVFS[opt] = struct{}{}
		}
	}

	mnts := make(map[int]*mountinfo.Info, len(info))
	for _, mi := range info {
		mnts[mi.ID] = mi
	}

	for _, mi := range info {
		if mi.Mountpoint != mnt {
			continue
		}

		// Use parent info as the defaults
		p := mnts[mi.Parent]
		pOpts := make(map[string]struct{})
		if p.Options != "" {
			for _, opt := range strings.Split(p.Options, ",") {
				pOpts[clean(opt)] = struct{}{}
			}
		}
		pOptional := make(map[string]struct{})
		if p.Optional != "" {
			for _, field := range strings.Split(p.Optional, ",") {
				pOptional[clean(field)] = struct{}{}
			}
		}

		// Validate Options
		if mi.Options != "" {
			for _, opt := range strings.Split(mi.Options, ",") {
				opt = clean(opt)
				if !has(wantedOpts, opt) && !has(pOpts, opt) && opt != "relatime" {
					t.Errorf("unexpected mount option %q, expected %q", opt, opts)
				}
				delete(wantedOpts, opt)
			}
		}
		for opt := range wantedOpts {
			t.Errorf("missing mount option %q, found %q", opt, mi.Options)
		}

		// Validate Optional
		if mi.Optional != "" {
			for _, field := range strings.Split(mi.Optional, ",") {
				field = clean(field)
				if !has(wantedOptional, field) && !has(pOptional, field) {
					t.Errorf("unexpected optional field %q, expected %q", field, optional)
				}
				delete(wantedOptional, field)
			}
		}
		for field := range wantedOptional {
			t.Errorf("missing optional field %q, found %q", field, mi.Optional)
		}

		// Validate VFS if set
		if vfs != "" {
			if mi.VFSOptions != "" {
				for _, opt := range strings.Split(mi.VFSOptions, ",") {
					opt = clean(opt)
					if !has(wantedVFS, opt) &&
						opt != "seclabel" && // can be added by selinux
						opt != "inode64" && opt != "inode32" { // can be added by kernel 5.9+
						t.Errorf("unexpected vfs option %q, expected %q", opt, vfs)
					}
					delete(wantedVFS, opt)
				}
			}
			for opt := range wantedVFS {
				t.Errorf("missing vfs option %q, found %q", opt, mi.VFSOptions)
			}
		}

		return
	}

	t.Errorf("failed to find mount %q", mnt)
}

// clean strips off any value param after the colon
func clean(v string) string {
	return strings.SplitN(v, ":", 2)[0]
}

// has returns true if key is a member of m
func has(m map[string]struct{}, key string) bool {
	_, ok := m[key]
	return ok
}
