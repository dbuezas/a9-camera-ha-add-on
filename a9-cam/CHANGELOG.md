# Changelog

## 3.0.0

- Rewrite in Go. No Python and no FFmpeg in the image anymore.
- New: RTSP on port 8556 with MJPEG video and PCMA audio, for go2rtc and the Generic Camera integration.
- One camera stream is shared by all viewers. The camera stops sending when nobody watches.
- The data channel restarts by itself when the video stops.
- New web UI with live view and buttons for IR, flip, LED and reboot.
- Option `log_level`.
- Removed: `/dev/CAMERA_ID/stream`, `/go2rtc-stream`, `/browser-stream` and `/audio`. Use the RTSP URL.

## 2.4

- Last Python version.
