package main

import (
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/baidu/openedge/logger"
	openedge "github.com/baidu/openedge/sdk/openedge-go"
	"github.com/baidu/openedge/utils"
)

type cleaner struct {
	count  int32
	prefix string
	target string
	last   *openedge.AppConfig
	log    logger.Logger
	sync.Mutex
}

func newCleaner(prefix, target string, log logger.Logger) *cleaner {
	return &cleaner{
		count:  3,
		prefix: prefix,
		target: target,
		log:    log,
	}
}

func (c *cleaner) reset(prepare func(openedge.VolumeInfo) (*openedge.AppConfig, string, error), cfg openedge.VolumeInfo) (*openedge.AppConfig, string, error) {
	c.Lock()
	defer c.Unlock()
	appcfg, hostdir, err := prepare(cfg)
	c.count = 3
	c.last = appcfg
	return appcfg, hostdir, err
}

func (c *cleaner) do(version string) {
	if version == "" {
		c.log.Debugf("report version is empty")
		return
	}

	c.Lock()
	defer c.Unlock()
	// not clean if last app config is not cached,
	// for example, when agent is restarted
	if c.last == nil {
		c.log.Debugf("last app config is not cached")
		return
	}
	// not clean if last app config version is not matched,
	// for example, openedge master reload task is not finised or failed.
	if c.last.Version != version {
		c.log.Debugf("report version is not matched")
		return
	}
	// delay three reporting cycles and then clean up
	c.count--
	if c.count != 0 {
		return
	}

	c.log.Infof("start to clean '%s'", c.target)
	defer utils.Trace("end to clean,", c.log.Infof)()

	// list folders to remove
	remove, err := list(c.prefix, c.target, c.last.Volumes)
	if err != nil {
		c.log.WithError(err).Warnf("failed to list old volumes")
		return
	}
	for _, v := range remove {
		err := os.RemoveAll(v)
		if err != nil {
			c.log.WithError(err).Warnf("failed to remove old volumes")
		}
		c.log.Infof("old volume is removed: %s", v)
	}
}

func list(prefix, target string, volumes []openedge.VolumeInfo) ([]string, error) {
	keep := map[string]bool{}
	for _, v := range volumes {
		// remove prefix from path
		p, err := filepath.Rel(prefix, v.Path)
		if err != nil {
			continue
		}
		ps := strings.Split(p, string(filepath.Separator))
		if len(ps) == 0 {
			// ignore the case that v.Path equals prefix
			continue
		}
		if ps[0] == ".." {
			// ignore the case that v.Path out of prefix
			continue
		}
		keep[ps[0]] = len(ps) > 1
	}
	infos, err := ioutil.ReadDir(target)
	if err != nil {
		return nil, err
	}
	remove := []string{}
	for _, info := range infos {
		// skip the files and only clean folders,
		if !info.IsDir() {
			continue
		}
		next, ok := keep[info.Name()]
		if !ok {
			remove = append(remove, filepath.Join(target, info.Name()))
		} else if next {
			nextremove, err := list(path.Join(prefix, info.Name()), path.Join(target, info.Name()), volumes)
			if err != nil {
				return nil, err
			}
			remove = append(remove, nextremove...)
		}
	}
	return remove, err
}
