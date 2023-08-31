# Nydus Tarfs Mode

`Nydus Tarfs Mode` or `Tarfs` is a working mode for Nydus Image, which uses tar files as Nydus data blobs instead of generating native Nydus data blobs.

### Enable Tarfs
`Nydus Tarfs Mode` is still an experiment feature, please edit the snapshotter configuration file to enable the feature:
```
[experimental.tarfs]
enable_tarfs = true
```

### Generate Raw Disk Image for Each Layer of a Container Image
`Tarfs` supports generating a raw disk image for each layer of a container image, which can be directly mounted as EROFS filesystem through loopdev. Please edit the snapshotter configuration file to enable this submode:
```
[experimental.tarfs]
enable_tarfs = true
export_mode = "layer_block"
```

This is an example to generate and verify raw disk image for each layer of a container image:
```
$ containerd-nydus-grpc --config /etc/nydus/config.toml &
$ nerdctl run --snapshotter nydus --rm nginx

# Show mounted rootfs a container
$ mount
/dev/loop17 on /var/lib/containerd-nydus/snapshots/7/mnt type erofs (ro,relatime,user_xattr,acl,cache_strategy=readaround)

# Show loop devices used to mount layers and bootstrap for a container image
$ losetup 
NAME SIZELIMIT OFFSET AUTOCLEAR RO BACK-FILE                                                                       DIO LOG-SEC
/dev/loop11 0      0         0  0 /var/lib/containerd-nydus/cache/fd9f026c631046113bd492f69761c3ba6042c791c35a60e7c7f3b8f254592daa 0     512
/dev/loop12 0      0         0  0 /var/lib/containerd-nydus/cache/055fa98b43638b67d10c58d41094d99c8696cc34b7a960c7a0cc5d9d152d12b3 0     512
/dev/loop13 0      0         0  0 /var/lib/containerd-nydus/cache/96576293dd2954ff84251aa0455687c8643358ba1b190ea1818f56b41884bdbd 0     512
/dev/loop14 0      0         0  0 /var/lib/containerd-nydus/cache/a7c4092be9044bd4eef78f27c95785ef3a9f345d01fd4512bc94ddaaefc359f4 0     512
/dev/loop15 0      0         0  0 /var/lib/containerd-nydus/cache/e3b6889c89547ec9ba653ab44ed32a99370940d51df956968c0d578dd61ab665 0     512
/dev/loop16 0      0         0  0 /var/lib/containerd-nydus/cache/da761d9a302b21dc50767b67d46f737f5072fb4490c525b4a7ae6f18e1dbbf75 0     512
/dev/loop17 0      0         0  0 /var/lib/containerd-nydus/snapshots/7/fs/image/image.boot                         0     512

# Files without suffix are tar files, files with suffix `layer.disk` are raw disk image for container image layers
$ ls -l /var/lib/containerd-nydus/cache/
total 376800
-rw-r--r-- 1 root root      3584 Aug 30 23:18 055fa98b43638b67d10c58d41094d99c8696cc34b7a960c7a0cc5d9d152d12b3
-rw-r--r-- 1 root root    527872 Aug 30 23:18 055fa98b43638b67d10c58d41094d99c8696cc34b7a960c7a0cc5d9d152d12b3.layer.disk
-rw-r--r-- 1 root root  77814784 Aug 30 23:18 52d2b7f179e32b4cbd579ee3c4958027988f9a8274850ab0c7c24661e3adaac5
-rw-r--r-- 1 root root  78863360 Aug 30 23:18 52d2b7f179e32b4cbd579ee3c4958027988f9a8274850ab0c7c24661e3adaac5.layer.disk
-rw-r--r-- 1 root root      4608 Aug 30 23:18 96576293dd2954ff84251aa0455687c8643358ba1b190ea1818f56b41884bdbd
-rw-r--r-- 1 root root    528896 Aug 30 23:18 96576293dd2954ff84251aa0455687c8643358ba1b190ea1818f56b41884bdbd.layer.disk
-rw-r--r-- 1 root root      2560 Aug 30 23:18 a7c4092be9044bd4eef78f27c95785ef3a9f345d01fd4512bc94ddaaefc359f4
-rw-r--r-- 1 root root    526848 Aug 30 23:18 a7c4092be9044bd4eef78f27c95785ef3a9f345d01fd4512bc94ddaaefc359f4.layer.disk
-rw-r--r-- 1 root root      7168 Aug 30 23:18 da761d9a302b21dc50767b67d46f737f5072fb4490c525b4a7ae6f18e1dbbf75
-rw-r--r-- 1 root root    531456 Aug 30 23:18 da761d9a302b21dc50767b67d46f737f5072fb4490c525b4a7ae6f18e1dbbf75.layer.disk
-rw-r--r-- 1 root root      5120 Aug 30 23:18 e3b6889c89547ec9ba653ab44ed32a99370940d51df956968c0d578dd61ab665
-rw-r--r-- 1 root root    529408 Aug 30 23:18 e3b6889c89547ec9ba653ab44ed32a99370940d51df956968c0d578dd61ab665.layer.disk
-rw-r--r-- 1 root root 112968704 Aug 30 23:18 fd9f026c631046113bd492f69761c3ba6042c791c35a60e7c7f3b8f254592daa
-rw-r--r-- 1 root root 113492992 Aug 30 23:18 fd9f026c631046113bd492f69761c3ba6042c791c35a60e7c7f3b8f254592daa.layer.disk
$ file /var/lib/containerd-nydus/cache/055fa98b43638b67d10c58d41094d99c8696cc34b7a960c7a0cc5d9d152d12b3
/var/lib/containerd-nydus/cache/055fa98b43638b67d10c58d41094d99c8696cc34b7a960c7a0cc5d9d152d12b3: POSIX tar archive

# Mount the raw disk image for a container image layer
$ losetup /dev/loop100 /var/lib/containerd-nydus/cache/055fa98b43638b67d10c58d41094d99c8696cc34b7a960c7a0cc5d9d152d12b3.layer.disk 
$ mount -t erofs /dev/loop100 ./mnt/
$ mount
tmpfs on /run/user/0 type tmpfs (rw,nosuid,nodev,relatime,size=1544836k,nr_inodes=386209,mode=700,inode64)
/dev/loop17 on /var/lib/containerd-nydus/snapshots/7/mnt type erofs (ro,relatime,user_xattr,acl,cache_strategy=readaround)
/dev/loop100 on /root/ws/nydus-snapshotter.git/mnt type erofs (ro,relatime,user_xattr,acl,cache_strategy=readaround)

```

### Generate Raw Disk Image for a Container Image
`Tarfs` supports generating a raw disk image a container image, which can be directly mounted as EROFS filesystem through loopdev. Please edit the snapshotter configuration file to enable this submode:
```
[experimental.tarfs]
enable_tarfs = true
export_mode = "image_block"
```

This is an example to generate and verify raw disk image for a container image:
```
$ containerd-nydus-grpc --config /etc/nydus/config.toml &
$ nerdctl run --snapshotter nydus --rm nginx

# Files without suffix are tar files, files with suffix `image.disk` are raw disk image for a container image
$ ls -l /var/lib/containerd-nydus/cache/
total 376320
-rw-r--r-- 1 root root      3584 Aug 30 23:35 055fa98b43638b67d10c58d41094d99c8696cc34b7a960c7a0cc5d9d152d12b3
-rw-r--r-- 1 root root  77814784 Aug 30 23:35 52d2b7f179e32b4cbd579ee3c4958027988f9a8274850ab0c7c24661e3adaac5
-rw-r--r-- 1 root root      4608 Aug 30 23:35 96576293dd2954ff84251aa0455687c8643358ba1b190ea1818f56b41884bdbd
-rw-r--r-- 1 root root      2560 Aug 30 23:35 a7c4092be9044bd4eef78f27c95785ef3a9f345d01fd4512bc94ddaaefc359f4
-rw-r--r-- 1 root root      7168 Aug 30 23:35 da761d9a302b21dc50767b67d46f737f5072fb4490c525b4a7ae6f18e1dbbf75
-rw-r--r-- 1 root root 194518016 Aug 30 23:36 da761d9a302b21dc50767b67d46f737f5072fb4490c525b4a7ae6f18e1dbbf75.image.disk
-rw-r--r-- 1 root root      5120 Aug 30 23:35 e3b6889c89547ec9ba653ab44ed32a99370940d51df956968c0d578dd61ab665
-rw-r--r-- 1 root root 112968704 Aug 30 23:36 fd9f026c631046113bd492f69761c3ba6042c791c35a60e7c7f3b8f254592daa

```

### Generate Raw Disk Image with dm-verity Information
`Tarfs` supports generating raw disk images with dm-verity information, to enable runtime data integrity validation. Please change `export_mode` in snapshotter configuration file to `layer_block_with_verity` or `image_block_with_verity`.

```
[experimental.tarfs]
enable_tarfs = true
export_mode = "image_block_with_verity"
```

This is an example to generate and verify raw disk image for a container image with dm-verity information:
```
$ containerd-nydus-grpc --config /etc/nydus/config.toml &
$ nerdctl run --snapshotter nydus --rm nginx

# Files without suffix are tar files, files with suffix `image.disk` are raw disk image for a container image
$ ls -l /var/lib/containerd-nydus/cache/
total 388296
-rw-r--r-- 1 root root      3584 Aug 30 23:45 055fa98b43638b67d10c58d41094d99c8696cc34b7a960c7a0cc5d9d152d12b3
-rw-r--r-- 1 root root  77814784 Aug 30 23:46 52d2b7f179e32b4cbd579ee3c4958027988f9a8274850ab0c7c24661e3adaac5
-rw-r--r-- 1 root root      4608 Aug 30 23:45 96576293dd2954ff84251aa0455687c8643358ba1b190ea1818f56b41884bdbd
-rw-r--r-- 1 root root      2560 Aug 30 23:45 a7c4092be9044bd4eef78f27c95785ef3a9f345d01fd4512bc94ddaaefc359f4
-rw-r--r-- 1 root root      7168 Aug 30 23:45 da761d9a302b21dc50767b67d46f737f5072fb4490c525b4a7ae6f18e1dbbf75
-rw-r--r-- 1 root root 206782464 Aug 30 23:46 da761d9a302b21dc50767b67d46f737f5072fb4490c525b4a7ae6f18e1dbbf75.image.disk
-rw-r--r-- 1 root root      5120 Aug 30 23:45 e3b6889c89547ec9ba653ab44ed32a99370940d51df956968c0d578dd61ab665
-rw-r--r-- 1 root root 112968704 Aug 30 23:46 fd9f026c631046113bd492f69761c3ba6042c791c35a60e7c7f3b8f254592daa

$ losetup /dev/loop100 /var/lib/containerd-nydus/cache/da761d9a302b21dc50767b67d46f737f5072fb4490c525b4a7ae6f18e1dbbf75.image.disk
$  veritysetup open --no-superblock --format=1 -s "" --hash=sha256 --data-block-size=512 --hash-block-size=4096 --data-blocks 379918 --hash-offset 194519040 /dev/loop100 image1 /dev/loop100 8113799aaf9a5d14feca1eadc3b7e6ea98bdaf61e3a2e4a8ef8c24e26a551efd
$ lsblk
loop100   7:100  0 197.2M  0 loop  
└─dm-0  252:0    0 185.5M  1 crypt 

$ veritysetup status dm-0
/dev/mapper/dm-0 is active and is in use.
  type:        VERITY
  status:      verified
  hash type:   1
  data block:  512
  hash block:  4096
  hash name:   sha256
  salt:        -
  data device: /dev/loop100
  data loop:   /var/lib/containerd-nydus/cache/da761d9a302b21dc50767b67d46f737f5072fb4490c525b4a7ae6f18e1dbbf75.image.disk
  size:        379918 sectors
  mode:        readonly
  hash device: /dev/loop100
  hash loop:   /var/lib/containerd-nydus/cache/da761d9a302b21dc50767b67d46f737f5072fb4490c525b4a7ae6f18e1dbbf75.image.disk
  hash offset: 379920 sectors
  root hash:   8113799aaf9a5d14feca1eadc3b7e6ea98bdaf61e3a2e4a8ef8c24e26a551efd

$ mount -t erofs /dev/dm-0 ./mnt/
mount: /root/ws/nydus-snapshotter.git/mnt: WARNING: source write-protected, mounted read-only.
$ ls -l mnt/
total 14
lrwxrwxrwx  1 root root    7 Aug 14 08:00 bin -> usr/bin
drwxr-xr-x  2 root root   27 Jul 15 00:00 boot
drwxr-xr-x  2 root root   27 Aug 14 08:00 dev
drwxr-xr-x  2 root root  184 Aug 16 17:50 docker-entrypoint.d
-rwxrwxr-x  1 root root 1620 Aug 16 17:50 docker-entrypoint.sh
drwxr-xr-x 34 root root 1524 Aug 16 17:50 etc
drwxr-xr-x  2 root root   27 Jul 15 00:00 home
lrwxrwxrwx  1 root root    7 Aug 14 08:00 lib -> usr/lib
lrwxrwxrwx  1 root root    9 Aug 14 08:00 lib32 -> usr/lib32
lrwxrwxrwx  1 root root    9 Aug 14 08:00 lib64 -> usr/lib64
lrwxrwxrwx  1 root root   10 Aug 14 08:00 libx32 -> usr/libx32
drwxr-xr-x  2 root root   27 Aug 14 08:00 media
drwxr-xr-x  2 root root   27 Aug 14 08:00 mnt
drwxr-xr-x  2 root root   27 Aug 14 08:00 opt
drwxr-xr-x  2 root root   27 Jul 15 00:00 proc
drwx------  2 root root   66 Aug 14 08:00 root
drwxr-xr-x  3 root root   43 Aug 14 08:00 run
lrwxrwxrwx  1 root root    8 Aug 14 08:00 sbin -> usr/sbin
drwxr-xr-x  2 root root   27 Aug 14 08:00 srv
drwxr-xr-x  2 root root   27 Jul 15 00:00 sys
drwxrwxrwt  2 root root   27 Aug 16 17:50 tmp
drwxr-xr-x 14 root root  229 Aug 14 08:00 usr
drwxr-xr-x 11 root root  204 Aug 14 08:00 var

```