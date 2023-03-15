/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

use std::{
    env, ffi, fs, io,
    io::Write,
    mem,
    os::unix::io::AsRawFd,
    path::Path,
    process, slice,
    sync::{Arc, Mutex},
    thread,
};

use chrono::prelude::Utc;
use lazy_static::lazy_static;
use libc::{__s32, __u16, __u32, __u64, __u8};
use nix::{
    poll::{poll, PollFd, PollFlags},
    sched::{setns, CloneFlags},
    unistd::{
        fork, getpgid,
        ForkResult::{Child, Parent},
    },
};
use serde::Serialize;
use signal_hook::{consts::SIGTERM, iterator::Signals};

#[derive(Debug, Clone, Copy)]
#[repr(C)]
struct fanotify_event {
    event_len: __u32,
    vers: __u8,
    reserved: __u8,
    metadata_len: __u16,
    mask: __u64,
    fd: __s32,
    pid: __s32,
}

#[derive(Serialize, Debug)]
struct EventInfo {
    path: String,
    size: u64,
    timestamp: i64,
}

impl PartialEq for EventInfo {
    fn eq(&self, target: &EventInfo) -> bool {
        self.path == target.path
    }
}

lazy_static! {
    static ref FAN_EVENT_METADATA_LEN: usize = mem::size_of::<fanotify_event>();
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

#[derive(Debug)]
enum SetnsError {
    IO(io::Error),
    Nix(nix::Error),
}

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

fn get_fd_path(fd: i32) -> Option<String> {
    let fd_path = format!("/proc/self/fd/{fd}");
    fs::read_link(fd_path).map_or(None, |path| Some(path.to_string_lossy().to_string()))
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

fn read_fanotify(fanotify_fd: i32) -> Vec<fanotify_event> {
    let mut vec = Vec::new();
    unsafe {
        let buffer = libc::malloc(*FAN_EVENT_METADATA_LEN * 1024);
        let sizeof = libc::read(fanotify_fd, buffer, *FAN_EVENT_METADATA_LEN * 1024);
        let src = slice::from_raw_parts(
            buffer as *mut fanotify_event,
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

fn generate_event_info(path: &str) -> Result<EventInfo, io::Error> {
    fs::metadata(path).map(|metadata| EventInfo {
        path: path.to_string(),
        size: metadata.len(),
        timestamp: Utc::now().timestamp_micros(),
    })
}

fn send_event_info(event_info: &Vec<EventInfo>) -> Result<(), SendError> {
    let mut writer = io::stdout();
    serde_json::to_writer(writer.lock(), event_info).map_err(SendError::Serde)?;
    writer.flush().map_err(SendError::IO)
}

fn handle_fanotify_event(fd: i32) {
    let mut fds = [PollFd::new(fd.as_raw_fd(), PollFlags::POLLIN)];
    let event_info = Arc::new(Mutex::<Vec<EventInfo>>::new(Vec::default()));
    let local_event_info = event_info.clone();

    if let Err(e) = Signals::new([SIGTERM]).map(|mut signals| {
        thread::spawn(move || {
            for _ in signals.forever() {
                eprintln!("received SIGTERM signal, sending event information");
                if let Ok(event_info) = local_event_info.lock().as_deref() {
                    if let Err(e) = send_event_info(event_info) {
                        eprintln!("failed to send event information {event_info:?} {e:?}");
                    }
                }
                eprintln!("send event information successfully, exiting");
                process::exit(0);
            }
        })
    }) {
        eprintln!("failed to set SIGTERM signal handler {e:?}");
        return;
    }

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
                            if let Ok(path) = get_fd_path(event.fd) {
                                if let Ok(event_info) = event_info.lock().as_deref_mut() {
                                    if let Err(e) = generate_event_info(&path).map(|info| {
                                        if !event_info.contains(&info) {
                                            event_info.push(info)
                                        }
                                    }) {
                                        eprintln!(
                                            "failed to generate event information for {path:?} {e:?}"
                                        )
                                    };
                                }
                            }
                            // No matter the target path is valid or not, we should close the fd
                            close_fd(event.fd);
                        }
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

fn main() {
    if let Some(pid) = get_pid() {
        if let Err(e) = set_ns(format!("/proc/{pid}/ns/pid"), CloneFlags::CLONE_NEWPID)
            .map(|_| set_ns(format!("/proc/{pid}/ns/mnt"), CloneFlags::CLONE_NEWNS))
        {
            eprintln!("join namespace failed {e:?}");
            return;
        }
    }
    let pid = unsafe { fork() };
    match pid.expect("fork failed: unable to create child process") {
        Child => {
            if let Err(e) = init_fanotify().map(|fd| {
                mark_fanotify(fd, get_target().as_str()).map(|_| handle_fanotify_event(fd))
            }) {
                eprintln!("failed to start fanotify server {e:?}");
            }
        }
        Parent { child } => {
            if let Err(e) = getpgid(Some(child)).map(|pgid| {
                eprintln!("forked optimizer server subprocess, pid: {child}, pgid: {pgid}");
            }) {
                eprintln!("failed to get pgid of {child} {e:?}");
            };
        }
    }
}
