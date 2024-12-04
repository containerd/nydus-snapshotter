/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

use std::{
    env, ffi, fs, io,
    io::Write,
    mem,
    os::unix::{io::AsRawFd, net::UnixStream},
    path::{Path, PathBuf},
    slice,
    time::Instant,
};

use nix::{
    poll::{poll, PollFd, PollFlags},
    sched::{setns, CloneFlags},
    sys::wait::{waitpid, WaitStatus},
    unistd::{fork, getpgid, ForkResult},
};
use serde::Serialize;

use lazy_static::lazy_static;
use signal_hook::{consts::SIGTERM, low_level::pipe};

#[derive(Debug, Clone, Copy)]
#[repr(C)]
struct FanotifyEvent {
    event_len: u32,
    vers: u8,
    reserved: u8,
    metadata_len: u16,
    mask: u64,
    fd: i32,
    pid: i32,
}

#[derive(Serialize, Debug)]
struct EventInfo {
    path: String,
    size: u64,
    elapsed: u128,
}

impl PartialEq for EventInfo {
    fn eq(&self, target: &EventInfo) -> bool {
        self.path == target.path
    }
}

lazy_static! {
    static ref FAN_EVENT_METADATA_LEN: usize = mem::size_of::<FanotifyEvent>();
    static ref BEGIN_TIME: Instant = Instant::now();
}

const DEFAULT_TARGET: &str = "/";

const FAN_CLOEXEC: u32 = 0x0000_0001;
const FAN_NONBLOCK: u32 = 0x0000_0002;
const FAN_CLASS_CONTENT: u32 = 0x0000_0004;

const O_RDONLY: u32 = 0;
const O_LARGEFILE: u32 = 0;

const FAN_MARK_ADD: u32 = 0x0000_0001;
const FAN_MARK_MOUNT: u32 = 0x0000_0010;

const FAN_ACCESS: u64 = 0x0000_0001;
const FAN_OPEN: u64 = 0x0000_0020;
const FAN_OPEN_EXEC: u64 = 0x00001000;
const AT_FDCWD: i32 = -100;

#[allow(dead_code)]
#[derive(Debug)]
enum SetnsError {
    IO(io::Error),
    Nix(nix::Error),
}

#[allow(dead_code)]
#[derive(Debug)]
enum SendError {
    IO(io::Error),
    Serde(serde_json::Error),
}

fn get_pid() -> Option<String> {
    env::var("_MNTNS_PID").ok()
}

fn get_target() -> String {
    env::var("_TARGET").map_or(DEFAULT_TARGET.to_string(), |str| str)
}

fn get_fd_path(fd: i32) -> io::Result<PathBuf> {
    let fd_path = format!("/proc/self/fd/{fd}");
    fs::read_link(fd_path)
}

fn set_ns(ns_path: String, flags: CloneFlags) -> Result<(), SetnsError> {
    let file = fs::File::open(Path::new(ns_path.as_str())).map_err(SetnsError::IO)?;
    setns(file.as_raw_fd(), flags).map_err(SetnsError::Nix)
}

fn init_fanotify() -> Result<i32, io::Error> {
    unsafe {
        match libc::fanotify_init(
            FAN_CLOEXEC | FAN_CLASS_CONTENT | FAN_NONBLOCK,
            O_RDONLY | O_LARGEFILE,
        ) {
            -1 => Err(io::Error::last_os_error()),
            fd => Ok(fd),
        }
    }
}

fn mark_fanotify(fd: i32, path: &str) -> Result<(), io::Error> {
    let path = ffi::CString::new(path)?;
    unsafe {
        match libc::fanotify_mark(
            fd,
            FAN_MARK_ADD | FAN_MARK_MOUNT,
            FAN_OPEN | FAN_ACCESS | FAN_OPEN_EXEC,
            AT_FDCWD,
            path.as_ptr(),
        ) {
            0 => Ok(()),
            _ => Err(io::Error::last_os_error()),
        }
    }
}

fn read_fanotify(fanotify_fd: i32) -> Vec<FanotifyEvent> {
    let mut vec = Vec::new();
    unsafe {
        let buffer = libc::malloc(*FAN_EVENT_METADATA_LEN * 1024);
        let sizeof = libc::read(fanotify_fd, buffer, *FAN_EVENT_METADATA_LEN * 1024);
        let src = slice::from_raw_parts(
            buffer as *mut FanotifyEvent,
            sizeof as usize / *FAN_EVENT_METADATA_LEN,
        );
        vec.extend_from_slice(src);
        libc::free(buffer);
    }
    vec
}

fn close_fd(fd: i32) {
    unsafe {
        libc::close(fd);
    }
}

fn generate_event_info(path: &Path) -> Result<EventInfo, io::Error> {
    fs::metadata(path).map(|metadata| EventInfo {
        path: path.to_string_lossy().to_string(),
        size: metadata.len(),
        elapsed: BEGIN_TIME.elapsed().as_micros(),
    })
}

fn send_event(event: &EventInfo) -> Result<(), SendError> {
    let mut writer = io::stdout();
    let event_string = serde_json::to_string(event).map_err(SendError::Serde)?;
    writer
        .write_all(format!("{event_string}\n").as_bytes())
        .map_err(SendError::IO)?;
    writer.flush().map_err(SendError::IO)
}

fn handle_event(event: &FanotifyEvent, event_duplicate: &mut Vec<String>) -> Result<(), SendError> {
    let path = get_fd_path(event.fd).map_err(SendError::IO)?;
    let info = generate_event_info(&path).map_err(SendError::IO)?;
    if !event_duplicate.contains(&info.path) {
        send_event(&info)?;
        event_duplicate.push(info.path);
    }
    Ok(())
}

fn handle_fanotify_event(fd: i32) {
    let mut event_duplicate = Vec::new();
    let (reader, writer) = match UnixStream::pair() {
        Ok((reader, writer)) => (reader, writer),
        Err(e) => {
            eprintln!("failed to create a pair of sockets: {e:?}");
            return;
        }
    };
    if let Err(e) = pipe::register(SIGTERM, writer) {
        eprintln!("failed to set SIGTERM signal handler {e:?}");
        return;
    }
    let mut fds = [
        PollFd::new(fd.as_raw_fd(), PollFlags::POLLIN),
        PollFd::new(reader.as_raw_fd(), PollFlags::POLLIN),
    ];

    loop {
        match poll(&mut fds, -1) {
            Ok(polled_num) => {
                if polled_num <= 0 {
                    eprintln!("polled_num <= 0!");
                    break;
                }

                if let Some(flag) = fds[0].revents() {
                    if flag.contains(PollFlags::POLLIN) {
                        let events = read_fanotify(fd);
                        for event in events {
                            if let Err(e) = handle_event(&event, &mut event_duplicate) {
                                eprintln!("failed to handle event {event:?} {e:?}")
                            };
                            // No matter the target path is valid or not, we should close the fd
                            close_fd(event.fd);
                        }
                    }
                }

                if let Some(flag) = fds[1].revents() {
                    if flag.contains(PollFlags::POLLIN) {
                        println!("received SIGTERM signal");
                        break;
                    }
                }
            }
            Err(e) => {
                if e == nix::Error::EINTR {
                    continue;
                }
                eprintln!("Poll error {:?}", e);
                break;
            }
        }
    }
}

fn start_fanotify() -> Result<(), io::Error> {
    let fd = init_fanotify()?;
    mark_fanotify(fd, get_target().as_str())?;
    handle_fanotify_event(fd);
    Ok(())
}

fn join_namespace(pid: String) -> Result<(), SetnsError> {
    set_ns(format!("/proc/{pid}/ns/pid"), CloneFlags::CLONE_NEWPID)?;
    set_ns(format!("/proc/{pid}/ns/mnt"), CloneFlags::CLONE_NEWNS)?;
    Ok(())
}

fn main() {
    if let Some(pid) = get_pid() {
        if let Err(e) = join_namespace(pid) {
            eprintln!("join namespace failed {e:?}");
            return;
        }
    }

    match unsafe { fork() } {
        Ok(ForkResult::Child) => {
            if let Err(e) = start_fanotify() {
                eprintln!("failed to start fanotify server {e:?}");
            }
        }
        Ok(ForkResult::Parent { child }) => {
            if let Err(e) = getpgid(Some(child)).map(|pgid| {
                eprintln!("forked optimizer server subprocess, pid: {child}, pgid: {pgid}");
            }) {
                eprintln!("failed to get pgid of {child} {e:?}");
            }

            match waitpid(child, None) {
                Ok(WaitStatus::Signaled(pid, signal, _)) => {
                    eprintln!("child process {pid} was killed by signal {signal}");
                }
                Ok(WaitStatus::Stopped(pid, signal)) => {
                    eprintln!("child process {pid} was stopped by signal {signal}");
                }
                Err(e) => {
                    eprintln!("failed to wait for child process: {e}");
                }
                _ => {}
            }
        }
        Err(e) => {
            eprintln!("fork failed: unable to create child process: {e:?}");
        }
    }
}
