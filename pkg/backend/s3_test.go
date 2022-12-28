/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package backend

import (
	"reflect"
	"testing"
)

func Test_newS3Backend(t *testing.T) {
	type args struct {
		rawConfig []byte
	}

	tests := []struct {
		name    string
		args    args
		want    *S3Backend
		wantErr bool
	}{
		{
			name: "test1, no error",
			args: args{
				rawConfig: []byte(`{
					"endpoint": "localhost:9000",
					"scheme": "http",
					"bucket_name": "nydus",
					"region": "us-east-1",
					"object_prefix": "path/to/my-registry/",
					"access_key_id": "minio",
					"access_key_secret": "minio123"
				}`),
			},
			want: &S3Backend{
				objectPrefix:       "path/to/my-registry/",
				bucketName:         "nydus",
				endpointWithScheme: "http://localhost:9000",
				region:             "us-east-1",
				accessKeySecret:    "minio123",
				accessKeyID:        "minio",
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newS3Backend(tt.args.rawConfig, false)
			if (err != nil) != tt.wantErr {
				t.Errorf("newS3Backend() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("newS3Backend() = %+#v\nwant %+#v\n\n", got, tt.want)
			}
		})
	}
}
