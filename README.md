#### Synopsis:
```shell script
./sshmirror ~/local/dir username@remote.server /remote/dir
```

#### Description:
A fast, continuous, one-directional (local⇒remove) filesystem synchronization client (over SSH).

Basic usage scenario - local development with constant uploading to remote server via SSH. So basically, a fast alternative to SFTP manager of Jetbrains IDEs.

#### Example usage:
- download binary for your OS and architecture [here](https://github.com/0leksandr/sshmirror/releases/latest) (please note: initially developed for Linux-amd64, not tested on other platforms, thus may not work properly there)
- ensure, that you have `ssh` and `rsync` binaries installed system-wide:
  ```shell script
  sudo apt-get install ssh rsync
  ```
- start program:
  ```shell script
  ./sshmirror \
      -i=~/.ssh/my_rsa \
      -e='(^\.git/|^\.idea/|~$)' \
      ~/myProject me@remote.server /var/www/html/myProject
  ```
  where:
  - `~/myProject` - LOCAL (source) directory to be read from
  - `me@remote.server` - address and username (if needed) for remote server
  - `/var/www/html/myProject` - REMOTE (destination) directory to be written to  
  (see all possible flags and description with `./sshmirror -h`)
- make some changes to files in your local directory (create/edit/move/delete)
- see them being reflected on remote server

#### Proc:
- speed (especially on slow connections). See sample benchmark:
  - switching between `git` branches with total difference of 30 files (550Kb)
    - PhpStorm: 1.5min
    - `sshmirror`: 3sec

#### Cons:
- one-directional. So, to check remote files, you'll have to use other tools
- no visible speed-up on frequent uploading of singular files (speed-up is ≈10%)

#### Features:
- using `rsync` for transferring files
- transferring in batches instead of one-by-one. General rules for grouping files in the queue into a batch are:
  - last modification was made 0.5sec ago
  - first modification was made 5sec ago, and since then new modifications occur constantly (without break for 0.5sec)
- using `ssh` "Master connection" feature to keep one constant connection. Thus, once-in-a-while uploads do not need to establish connection over again
- filesystem events (modifications) tracked by `fsnotify`. Credits: https://github.com/fsnotify/fsnotify

---

Please submit bugs reports to https://github.com/0leksandr/sshmirror/issues
