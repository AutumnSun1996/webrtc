# play-from-disk-ivf
play-from-disk-ivf demonstrates how to send video and to your browser from files saved to disk.

For an example of playing H264 from disk see [play-from-disk-h264](https://github.com/pion/example-webrtc-applications/tree/master/play-from-disk-h264)

## Instructions
### Create IVF named `output.ivf` that contains a VP8/VP9/AV1 track
```
ffmpeg -i $INPUT_FILE -g 30 -c:v libvpx -b:v 2M vp8.ivf
ffmpeg -i $INPUT_FILE -g 30 -c:v libvpx-vp9 -b:v 2M vp9.ivf
ffmpeg -i $INPUT_FILE -g 30 -c:v libaom-av1 -b:v 2M av1.ivf
```

**Note**: In the `ffmpeg` command which produces the .ivf file, the argument `-b:v 2M` specifies the video bitrate to be 2 megabits per second. We provide this default value to produce decent video quality, but if you experience problems with this configuration (such as dropped frames etc.), you can decrease this. See the [ffmpeg documentation](https://ffmpeg.org/ffmpeg.html#Options) for more information on the format of the value.

### Download play-from-disk-ivf

```
export GO111MODULE=on
go get github.com/pion/webrtc/v3/examples/play-from-disk-ivf
```

### Run play-from-disk-ivf

Run `play-from-disk-ivf`

### Open browser and point to http://localhost:8080/

### Select Codec And Hit `Start Session`
