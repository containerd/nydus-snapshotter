# oerdctl: optimized nerdctl command for containerd

`oerdctl` is a `nerdctl`-compatible cli to adapt the `nerdctl run` command for recording accessed files when task running.


## examples
###  basic usage
We can use `oerdctl run` just like `nerdctl run`. You don't need time to learn the command parameters, because they are the same as `nerdctl run`. When running a task, the accessed files list will be stored at the default disk path `accessed_files.txt`.
More information about `nerdctl run`: [nerdctl#whale-blue_square-nerdctl-run](https://github.com/containerd/nerdctl#whale-blue_square-nerdctl-run)
```shel
oerdctl run --rm --net host -it cr-cn-guilin-boe.volces.com/vke/golang
```

###  special parameters

We can specify recording time with the `--wait-time` option.

```shell=
oerdctl run --rm --net host --wait-time 30 nginx
```

Otherwise, we can specify a string line to wait for with `--wait-line`, just like:

```shell=
oerdctl run --rm --net host --wait-line "ready for start up" nginx
```
The `oerdctl run` will kill task when getting expect string line from stand output.

We can also specify `oerdctl run` to wait for a signal with the `--wait-signal` option. Just like:

```shell=
oerdctl run --rm --net host --wait-signal nginx
```
The `oerdctl run` will kill task when getting `SIGINT` (also know as `ctrl + C`) signal.


The other option `--output` is also available to specify the output path for storing accessed files.