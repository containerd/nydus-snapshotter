#!/usr/bin/env bash

path=$1

default_path=file_list_path.txt
if [[ $# -eq 0 ]]; then
    path=${default_path}
fi

files=($(cat ${path} | tr "\n" " "))
files_number=${#files[@]}
echo "file number: $files_number"

for file in "${files[@]}"; do
    file_size=$(stat -c%s "${file}")
    echo "file: ${file} size: ${file_size}"
    cat ${file} >/dev/null
done

echo "Read file list done."
