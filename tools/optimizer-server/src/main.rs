/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

use std::{env, ffi, fs, io, io::Write, mem, os::unix::io::AsRawFd, path::Path, slice};

use lazy_static::lazy_static;
use libc::{__s32, __u16, __u32, __u64, __u8};
use nix::{
    poll::{poll, PollFd, PollFlags},
    sched::{setns, CloneFlags},
    unistd::{
        fork,
        ForkResult::{Child, Parent},
    },
};

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

fn send_path(writer: &mut io::Stdout, path: String) -> Result<(), io::Error> {
    writer.write_all(path.as_bytes())?;
    writer.flush()
}

fn close_fd(fd: i32) {
    unsafe {
        libc::close(fd);
    }
}

fn handle_fanotify_event(fd: i32) {
    let mut writer = io::stdout();

    let mut fds = [PollFd::new(fd.as_raw_fd(), PollFlags::POLLIN)];
    loop {
        if let Ok(poll_num) = poll(&mut fds, -1) {
            if poll_num > 0 {
                if let Some(flag) = fds[0].revents() {
                    if flag.contains(PollFlags::POLLIN) {
                        let events = read_fanotify(fd);
                        for event in events {
                            if let Some(path) = get_fd_path(event.fd) {
                                if let Err(e) = send_path(&mut writer, path.clone() + "\n") {
                                    eprintln!("send path {path} failed: {e:?}");
                                };
                                close_fd(event.fd);
                            }
                        }
                    }
                }
            } else {
                eprintln!("poll_num <= 0!");
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
            eprintln!("forked optimizer server subprocess, pid: {child}");
        }
    }
}
