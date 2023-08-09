#!/bin/bash

NYDUS_VERSION=""
LOCAL_NYDUS_FILE_DIR=nydus/files

# pull vke-cluster-installer project
git clone git@code.byted.org:infcp/vke-cluster-installer.git
cd vke-cluster-installer
VKE_CLUSTER_INSTALLER_LATEST_BRANCH=origin/release-v$(git branch -r --list 'origin/release-*' | sed -e 's/origin\/release-v//g' | sed -e '/-/d' | sort -rV | head -n 1 | xargs)
git checkout $VKE_CLUSTER_INSTALLER_LATEST_BRANCH

# build nydusd
git clone git@code.byted.org:containerimage/image-service.git
cd image-service
NYDUSD_TAG=$(git describe --tags $(git rev-list --tags --max-count=1))
git checkout $NYDUSD_TAG
echo "[INFO] nydusd tag: $NYDUSD_TAG"
# the binaries are in image-service/output directory
./build.sh
cd ..

# build nydus-snapshotter
git clone git@code.byted.org:containerimage/nydus-snapshotter.git
cd nydus-snapshotter
NYDUS_SNAPSHOTTER_TAG=$(git describe --tags $(git rev-list --tags --max-count=1))
git checkout $NYDUS_SNAPSHOTTER_TAG
# the binaries are in nydus-snapshotter/output directory
./build.sh
cd ..

if [ -z $NYDUS_VERSION ]; then
    NYDUS_VERSION=$NYDUS_SNAPSHOTTER_TAG
fi

# nydus package name
NYDUS_FILE="nydus-$NYDUS_VERSION-linux-amd64.tar.gz"

mkdir -p $LOCAL_NYDUS_FILE_DIR
mkdir bin
cp image-service/output/* bin/
cp nydus-snapshotter/output/* bin/

echo "[INFO] nydus binary files: $(ls bin/)"

tar -zcvf $NYDUS_FILE bin
md5sum $NYDUS_FILE >md5sum.txt
mv $NYDUS_FILE md5sum.txt $LOCAL_NYDUS_FILE_DIR

rm -rf image-service nydus-snapshotter

echo "[INFO] nydus package files: $(ls $LOCAL_NYDUS_FILE_DIR)"

# build nydus package
make build-nydus-local PACKAGE_SAVE_PATH=../output LOCAL_DIR=$LOCAL_NYDUS_FILE_DIR

echo "" >../output/nydus_version
./bin/containerd-nydus-grpc --version >>../output/nydus_version
echo "" >>../output/nydus_version
./bin/nydusd --version >>../output/nydus_version
