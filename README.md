File Polling Directory Input
=======================  
Dynamic plugin loader for Heka's FilePollingInput plugins

Plugin Name: **FilePollingDirectoryInput**

The FilePollingDirectoryInput is largely based on Heka's [ProcessDirectoryInput](https://hekad.readthedocs.io/en/latest/config/inputs/processdir.html).  
It periodically scans a filesystem directory looking
for FilePollingInput configuration files. The FilePollingDirectoryInput will maintain
a pool of running FilePollingInputs based on the contents of this directory,
refreshing the set of running inputs as needed with every rescan. This allows
Heka administrators to manage a set of FilePollingInputs for a running
hekad server without restarting the server.

Each FilePollingDirectoryInput has a `files_dir` configuration setting, which is
the root folder of the tree where scheduled jobs are defined.
This folder must contain TOML files which specify the details
regarding which FilePollingInputs to run.

For example, a files_dir might look like this::


  - /usr/share/heka/files.d/
    - memprof.toml
    - cpuprof.toml

The names for each FilePolling input must be unique. Any duplicate named configs
will not be loaded.  
Ex.  

	[memprof]  
	type = "FilePollingInput"  
  	file_path = "/proc/meminfo"
	and  
	[cpuprof]  
	type = "FilePollingInput"
  	file_path = "/proc/cpuinfo"


Each config file must have a '.toml' extension. Each file which meets these criteria,
such as those shown in the example above, should contain the TOML configuration for exactly one
[FilePollingInput](https://hekad.readthedocs.io/en/latest/config/inputs/file_polling.html),
matching that of a standalone FilePollingInput with
the following restrictions:

- The section name OR type *must* be `FilePollingInput`. Any TOML sections named anything
  other than FilePollingInput will be ignored.


Config:

- ticker_interval (int, optional):
    Amount of time, in seconds, between scans of the files_dir. Defaults to
    300 (i.e. 5 minutes).
- files_dir (string, optional):
    This is the root folder of the tree where the scheduled jobs are defined.
    Absolute paths will be honored, relative paths will be computed relative to
    Heka's globally specified share_dir. Defaults to "files.d" (i.e.
    "$share_dir/files.d").
- retries (RetryOptions, optional):
    A sub-section that specifies the settings to be used for restart behavior
    of the FilePollingDirectoryInput (not the individual ProcessInputs, which are
    configured independently).
    See [Configuring Restarting Behavior](https://hekad.readthedocs.io/en/latest/config/index.html#configuring-restarting)

Example:

	[FilePollingDirectoryInput]
	files_dir = "/usr/share/heka/files.d"
	ticker_interval = 120

To Build
========

  See [Building *hekad* with External Plugins](http://hekad.readthedocs.org/en/latest/installing.html#build-include-externals)
  for compiling in plugins.

  Edit cmake/plugin_loader.cmake file and add

      add_external_plugin(git https://github.com/michaelgibson/heka-file-polling-directory-input master)

  Build Heka:
  	. ./build.sh
