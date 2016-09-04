package file

import (
	"errors"
	"fmt"
	"github.com/bbangert/toml"
	. "github.com/mozilla-services/heka/pipeline"
	"github.com/mozilla-services/heka/plugins/file"
	"os"
	"path/filepath"
)

type FilePollingEntry struct {
	ir       InputRunner
	maker    MutableMaker
	config   *file.FilePollingInputConfig
	fileName string
}

type FilePollingDirectoryInputConfig struct {
	// Root folder of the tree where the scheduled jobs are defined.
	FilePollingDir string `toml:"file_dir"`

	// Number of seconds to wait between scans of the job directory. Defaults
	// to 300.
	TickerInterval uint `toml:"ticker_interval"`
}

type FilePollingDirectoryInput struct {
	// The actual running InputRunners.
	inputs map[string]*FilePollingEntry
	// Set of InputRunners that should exist as specified by walking
	// the FilePolling directory.
	specified map[string]*FilePollingEntry
	stopChan  chan bool
	logDir    string
	ir        InputRunner
	h         PluginHelper
	pConfig   *PipelineConfig
}

// Helper function for manually comparing structs since slice attributes mean
// we can't use `==`.
func (lsdi *FilePollingDirectoryInput) Equals(runningEntry *file.FilePollingInputConfig, otherEntry *file.FilePollingInputConfig) bool {
	if runningEntry.FilePath != otherEntry.FilePath {
		return false
	}
	if runningEntry.TickerInterval != otherEntry.TickerInterval {
		return false
	}
	return true
}

// Heka will call this before calling any other methods to give us access to
// the pipeline configuration.
func (lsdi *FilePollingDirectoryInput) SetPipelineConfig(pConfig *PipelineConfig) {
	lsdi.pConfig = pConfig
}

func (lsdi *FilePollingDirectoryInput) Init(config interface{}) (err error) {
	conf := config.(*FilePollingDirectoryInputConfig)
	lsdi.inputs = make(map[string]*FilePollingEntry)
	lsdi.stopChan = make(chan bool)
	globals := lsdi.pConfig.Globals
	lsdi.logDir = filepath.Clean(globals.PrependShareDir(conf.FilePollingDir))
	return
}

// ConfigStruct implements the HasConfigStruct interface and sets defaults.
func (lsdi *FilePollingDirectoryInput) ConfigStruct() interface{} {
	return &FilePollingDirectoryInputConfig{
		FilePollingDir: "files.d",
		TickerInterval: 300,
	}
}

func (lsdi *FilePollingDirectoryInput) Stop() {
	close(lsdi.stopChan)
}

// CleanupForRestart implements the Restarting interface.
func (lsdi *FilePollingDirectoryInput) CleanupForRestart() {
	lsdi.Stop()
}

func (lsdi *FilePollingDirectoryInput) Run(ir InputRunner, h PluginHelper) (err error) {
	lsdi.ir = ir
	lsdi.h = h
	if err = lsdi.loadInputs(); err != nil {
		return
	}

	var ok = true
	ticker := ir.Ticker()

	for ok {
		select {
		case _, ok = <-lsdi.stopChan:
		case <-ticker:
			if err = lsdi.loadInputs(); err != nil {
				return
			}
		}
	}

	return
}

// Reload the set of running FilePollingInput InputRunners. Not reentrant, should
// only be called from the FilePollingDirectoryInput's main Run goroutine.
func (lsdi *FilePollingDirectoryInput) loadInputs() (err error) {
	dups := false
	var runningEntryInputName string

	// Clear out lsdi.specified and then populate it from the file tree.
	lsdi.specified = make(map[string]*FilePollingEntry)
	if err = filepath.Walk(lsdi.logDir, lsdi.logDirWalkFunc); err != nil {
		return
	}

	// Remove any running inputs that are no longer specified
	for name, entry := range lsdi.inputs {
		if _, ok := lsdi.specified[name]; !ok {
			lsdi.pConfig.RemoveInputRunner(entry.ir)
			delete(lsdi.inputs, name)
			lsdi.ir.LogMessage(fmt.Sprintf("Removed: %s", name))
		}
	}

	// Iterate through the specified inputs and activate any that are new or
	// have been modified.

	for name, newEntry := range lsdi.specified {

		//Check to see if duplicate input already exists with same name but different file location.
		//If so, do not load it as it confuses the InputRunner
		for runningInputName, runningInput := range lsdi.inputs {
			if newEntry.ir.Name() == runningInput.ir.Name() && newEntry.fileName != runningInput.fileName {
				runningEntryInputName = runningInput.ir.Name()
				dups = true
				lsdi.pConfig.RemoveInputRunner(runningInput.ir)
				lsdi.ir.LogMessage(fmt.Sprintf("Removed: %s", runningInputName))
				delete(lsdi.inputs, runningInputName)
				return fmt.Errorf("Duplicate Name: Input with name [%s] already exists. Not loading input file: %s", runningEntryInputName, name)
			}
		}

		if runningEntry, ok := lsdi.inputs[name]; ok {
			if (lsdi.Equals(runningEntry.config, newEntry.config) && runningEntry.ir.Name() == newEntry.ir.Name()) && !dups {
				// Nothing has changed, let this one keep running.
				continue
			}
			// It has changed, stop the old one.
			lsdi.pConfig.RemoveInputRunner(runningEntry.ir)
			lsdi.ir.LogMessage(fmt.Sprintf("Removed: %s", name))
			delete(lsdi.inputs, name)
		}

		// Start up a new input.
		if err = lsdi.pConfig.AddInputRunner(newEntry.ir); err != nil {
			lsdi.ir.LogError(fmt.Errorf("creating input '%s': %s", name, err))
			continue
		}
		lsdi.inputs[name] = newEntry
		lsdi.ir.LogMessage(fmt.Sprintf("Added: %s", name))
	}
	return
}

// Function of type filepath.WalkFunc, called repeatedly when we walk a
// directory tree using filepath.Walk. This function is not reentrant, it
// should only ever be triggered from the similarly not reentrant loadInputs
// method.
func (lsdi *FilePollingDirectoryInput) logDirWalkFunc(path string, info os.FileInfo,
	err error) error {

	if err != nil {
		lsdi.ir.LogError(fmt.Errorf("walking '%s': %s", path, err))
		return nil
	}
	// info == nil => filepath doesn't actually exist.
	if info == nil {
		return nil
	}
	// Skip directories or anything that doesn't end in `.toml`.
	if info.IsDir() || filepath.Ext(path) != ".toml" {
		return nil
	}

	// Things look good so far. Try to load the data into a config struct.
	var entry *FilePollingEntry
	if entry, err = lsdi.loadFilePollingFile(path); err != nil {
		lsdi.ir.LogError(fmt.Errorf("loading FilePollingInput file '%s': %s", path, err))
		return nil
	}

	// Override the config settings we manage, make the runner, and store the
	// entry.
	prepConfig := func() (interface{}, error) {
		config, err := entry.maker.OrigPrepConfig()
		if err != nil {
			return nil, err
		}
		filePollingInputConfig := config.(*file.FilePollingInputConfig)
		return filePollingInputConfig, nil
	}
	config, err := prepConfig()
	if err != nil {
		lsdi.ir.LogError(fmt.Errorf("prepping config: %s", err.Error()))
		return nil
	}
	entry.config = config.(*file.FilePollingInputConfig)
	entry.maker.SetPrepConfig(prepConfig)

	runner, err := entry.maker.MakeRunner("")
	if err != nil {
		lsdi.ir.LogError(fmt.Errorf("making runner: %s", err.Error()))
		return nil
	}

	entry.ir = runner.(InputRunner)
	entry.ir.SetTransient(true)
	entry.fileName = path
	lsdi.specified[path] = entry
	return nil
}

func (lsdi *FilePollingDirectoryInput) loadFilePollingFile(path string) (*FilePollingEntry, error) {
	var (
		err     error
		ok      = false
		section toml.Primitive
	)

	unparsedConfig := make(map[string]toml.Primitive)
	if _, err = toml.DecodeFile(path, &unparsedConfig); err != nil {
		return nil, err
	}
	for name, conf := range unparsedConfig {
		confName, confType, _ := lsdi.getConfigFileInfo(name, conf)
		if confType == "FilePollingInput" {
			ok = true
			section = conf
			path = confName
			continue
		}
	}

	if !ok {
		err = errors.New("No `FilePollingInput` section.")
		return nil, err
	}

	maker, err := NewPluginMaker("FilePollingInput", lsdi.pConfig, section)
	if err != nil {
		return nil, fmt.Errorf("can't create plugin maker: %s", err)
	}

	mutMaker := maker.(MutableMaker)
	mutMaker.SetName(path)

	prepCommonTypedConfig := func() (interface{}, error) {
		commonTypedConfig, err := mutMaker.OrigPrepCommonTypedConfig()
		if err != nil {
			return nil, err
		}
		commonInput := commonTypedConfig.(CommonInputConfig)
		commonInput.Retries = RetryOptions{
			MaxDelay:   "30s",
			Delay:      "250ms",
			MaxRetries: -1,
		}
		if commonInput.CanExit == nil {
			b := true
			commonInput.CanExit = &b
		}
		return commonInput, nil
	}
	mutMaker.SetPrepCommonTypedConfig(prepCommonTypedConfig)

	entry := &FilePollingEntry{
		maker: mutMaker,
	}
	return entry, nil
}

func (lsdi *FilePollingDirectoryInput) getConfigFileInfo(name string, configFile toml.Primitive) (configName string, configType string, configCategory string) {
	//Get identifiers from config section
	pipeConfig := NewPipelineConfig(nil)
	maker, _ := NewPluginMaker(name, pipeConfig, configFile)
	if maker.Type() != "" {
		return maker.Name(), maker.Type(), maker.Category()
	}
	return "", "", ""
}

func init() {
	RegisterPlugin("FilePollingDirectoryInput", func() interface{} {
		return new(FilePollingDirectoryInput)
	})
}
