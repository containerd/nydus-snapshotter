package main

import (
	"fmt"
	"log"
	"os"
	"path"
	"strings"
	"syscall"

	"github.com/pkg/errors"
	"github.com/urfave/cli/v2"
	"golang.org/x/sys/unix"
)

const (
	// Extra mount option to pass Nydus specific information from snapshotter to runtime through containerd.
	extraOptionKey = "extraoption="
	// Kata virtual volume infmation passed from snapshotter to runtime through containerd, superset of `extraOptionKey`.
	// Please refer to `KataVirtualVolume` in https://github.com/kata-containers/kata-containers/blob/main/src/libs/kata-types/src/mount.rs
	kataVolumeOptionKey = "io.katacontainers.volume="
)

var (
	Version   = "v0.1"
	BuildTime = "unknown"
	pagesize  = os.Getpagesize()
)

/*
containerd run fuse.mount format: nydus-overlayfs overlay /tmp/ctd-volume107067851
-o lowerdir=/foo/lower2:/foo/lower1,upperdir=/foo/upper,workdir=/foo/work,extraoption={...},dev,suid]
*/
type mountArgs struct {
	fsType  string
	target  string
	options []string
}

func parseArgs(args []string) (*mountArgs, error) {
	margs := &mountArgs{
		fsType: args[0],
		target: args[1],
	}
	if margs.fsType != "overlay" {
		return nil, errors.Errorf("invalid filesystem type %s for overlayfs", margs.fsType)
	}
	if len(margs.target) == 0 {
		return nil, errors.New("empty overlayfs mount target")
	}

	if args[2] == "-o" && len(args[3]) != 0 {
		for _, opt := range strings.Split(args[3], ",") {
			// filter Nydus specific options
			if strings.HasPrefix(opt, extraOptionKey) || strings.HasPrefix(opt, kataVolumeOptionKey) {
				continue
			}
			margs.options = append(margs.options, opt)
		}
	}
	if len(margs.options) == 0 {
		return nil, errors.New("empty overlayfs mount options")
	}

	return margs, nil
}

func parseOptions(options []string) (int, string) {
	flagsTable := map[string]int{
		"async":         unix.MS_SYNCHRONOUS,
		"atime":         unix.MS_NOATIME,
		"bind":          unix.MS_BIND,
		"defaults":      0,
		"dev":           unix.MS_NODEV,
		"diratime":      unix.MS_NODIRATIME,
		"dirsync":       unix.MS_DIRSYNC,
		"exec":          unix.MS_NOEXEC,
		"mand":          unix.MS_MANDLOCK,
		"noatime":       unix.MS_NOATIME,
		"nodev":         unix.MS_NODEV,
		"nodiratime":    unix.MS_NODIRATIME,
		"noexec":        unix.MS_NOEXEC,
		"nomand":        unix.MS_MANDLOCK,
		"norelatime":    unix.MS_RELATIME,
		"nostrictatime": unix.MS_STRICTATIME,
		"nosuid":        unix.MS_NOSUID,
		"rbind":         unix.MS_BIND | unix.MS_REC,
		"relatime":      unix.MS_RELATIME,
		"remount":       unix.MS_REMOUNT,
		"ro":            unix.MS_RDONLY,
		"rw":            unix.MS_RDONLY,
		"strictatime":   unix.MS_STRICTATIME,
		"suid":          unix.MS_NOSUID,
		"sync":          unix.MS_SYNCHRONOUS,
	}

	var (
		flags int
		data  []string
	)
	for _, o := range options {
		if f, exist := flagsTable[o]; exist {
			flags |= f
		} else {
			data = append(data, o)
		}
	}
	return flags, strings.Join(data, ",")
}

// longestCommonPrefix finds the longest common prefix in the string slice.
func longestCommonPrefix(strs []string) string {
	if len(strs) == 0 {
		return ""
	} else if len(strs) == 1 {
		return strs[0]
	}

	min, max := strs[0], strs[0]
	for _, str := range strs[1:] {
		if min > str {
			min = str
		}
		if max < str {
			max = str
		}
	}

	for i := 0; i < len(min) && i < len(max); i++ {
		if min[i] != max[i] {
			return min[:i]
		}
	}
	return min
}

// compactLowerdirOption shortens lowerdir paths by extracting the longest
// common directory prefix across all lowerdirs, upperdir, and workdir. The
// caller chdir's into that prefix before calling mount so the kernel receives
// shorter, relative paths that fit within the mount-data page-size limit.
// Ported from containerd's core/mount/mount_linux.go.
func compactLowerdirOption(options []string) (string, []string) {
	var lowerdirIdx = -1
	for i, opt := range options {
		if strings.HasPrefix(opt, "lowerdir=") {
			lowerdirIdx = i
			break
		}
	}
	if lowerdirIdx == -1 {
		return "", options
	}

	dirs := strings.Split(options[lowerdirIdx][len("lowerdir="):], ":")
	if len(dirs) <= 1 {
		return "", options
	}

	// Collect all paths that participate in the overlay: lowerdirs + upperdir + workdir.
	allPaths := make([]string, 0, len(dirs)+2)
	allPaths = append(allPaths, dirs...)
	for _, opt := range options {
		if strings.HasPrefix(opt, "upperdir=") {
			allPaths = append(allPaths, opt[len("upperdir="):])
		} else if strings.HasPrefix(opt, "workdir=") {
			allPaths = append(allPaths, opt[len("workdir="):])
		}
	}

	commondir := longestCommonPrefix(allPaths)
	if commondir == "" {
		return "", options
	}

	// Back up to the parent directory to avoid partial snapshot-id matches.
	commondir = path.Dir(commondir)
	if commondir == "/" || commondir == "." {
		return "", options
	}
	commondir += "/"

	// Rebuild options with relative paths.
	newopts := make([]string, 0, len(options))
	for i, opt := range options {
		switch {
		case i == lowerdirIdx:
			newdirs := make([]string, 0, len(dirs))
			for _, dir := range dirs {
				if len(dir) <= len(commondir) {
					return "", options
				}
				newdirs = append(newdirs, dir[len(commondir):])
			}
			newopts = append(newopts, fmt.Sprintf("lowerdir=%s", strings.Join(newdirs, ":")))
		case strings.HasPrefix(opt, "upperdir="):
			p := opt[len("upperdir="):]
			if len(p) <= len(commondir) {
				return "", options
			}
			newopts = append(newopts, fmt.Sprintf("upperdir=%s", p[len(commondir):]))
		case strings.HasPrefix(opt, "workdir="):
			p := opt[len("workdir="):]
			if len(p) <= len(commondir) {
				return "", options
			}
			newopts = append(newopts, fmt.Sprintf("workdir=%s", p[len(commondir):]))
		default:
			newopts = append(newopts, opt)
		}
	}
	return commondir, newopts
}

func run(args cli.Args) error {
	margs, err := parseArgs(args.Slice())
	if err != nil {
		return errors.Wrap(err, "parse mount options")
	}

	flags, data := parseOptions(margs.options)

	// When the mount data string exceeds the kernel's page-size limit, compact
	// overlay paths by chdir'ing into their common prefix and using relative
	// paths — the same approach containerd uses for direct overlay mounts.
	var chdir string
	if len(data) >= pagesize-512 {
		chdir, margs.options = compactLowerdirOption(margs.options)
		if chdir != "" {
			_, data = parseOptions(margs.options)
		}
	}

	if chdir != "" {
		if err := os.Chdir(chdir); err != nil {
			return errors.Wrapf(err, "chdir to common overlay dir %s", chdir)
		}
	}

	err = syscall.Mount(margs.fsType, margs.target, margs.fsType, uintptr(flags), data)
	if err != nil {
		return errors.Wrapf(err, "mount overlayfs by syscall")
	}
	return nil
}

func main() {
	app := &cli.App{
		Name:      "NydusOverlayfs",
		Usage:     "FUSE mount helper for containerd to filter out Nydus specific options",
		Version:   fmt.Sprintf("%s.%s", Version, BuildTime),
		UsageText: "[Usage]: nydus-overlayfs overlay <target> -o <options>",
		Action: func(c *cli.Context) error {
			return run(c.Args())
		},
		Before: func(c *cli.Context) error {
			if c.NArg() != 4 {
				cli.ShowAppHelpAndExit(c, 1)
			}
			return nil
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}

	os.Exit(0)
}
