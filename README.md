# a9 add on

<img width="826" alt="image" src="https://github.com/dbuezas/a9-camera-ha-add-on/assets/777196/8ea61525-02b7-40ac-8853-0c2f63285a2d">

## Instructions

Until I make give this the correct structure to be installed in a standard way, the steps are:

1. Get the camera to connect to your access point (with the app or following instructions in https://github.com/intx82/a9-v720)
2. Reroute \*.naxclow.com to your HA computer IP (e.g using the AdGuard addon and configuring your router to use that as DNS provider)
3. Download all the code in here and put it inside the `~/addons/a9-v720`
4. [![Open your Home Assistant instance and show the Supervisor add-on store.](https://my.home-assistant.io/badges/supervisor_store.svg)](https://my.home-assistant.io/redirect/supervisor_store/)
5. Click on the three dots overflow menu on the top right, then `Check for updates`
6. There should now be a "A9 Fake camera server" addon.
7. Install and start it.
8. (restart the camera if you have one with newer FW version)
9. Go the the Addon UI and you are done!

### go2rtc

1. Learn & Install the Go2rpc addon, and WebRTC custom card
2. in go2rtc.yaml, add:

```yaml
streams:
  v9_camera: ffmpeg:http://127.0.0.1:80/dev/[your-cam_id]/go2rtc-stream#video=h264#audio=copy
```

11. You can use `v9_camera` in your WebRTC cards now.

## ToDo:

- [x] Implement audio streamiming from STA mode
- [x] Create an endpoint with merged video and audio with ffmpeg and named pipes
- [x] Remove OpenCV requirement so the server can run in an Alpine docker base
- [x] Find the correct way to configure ffmpeg to interprete the raw streams correctly
- [x] Expose the commands to toggle IR mode and other options via UDP
- [ ] Expose status and toggles as entities
- [ ] Make the structure of this repo compliant so it can be installed more easily
- [x] Find out how to get the low delay of opuslib without getting broken audio.

## Credits & details

This addon https://github.com/dbuezas/a9-camera-ha-add-on

Python code derived from https://github.com/intx82/a9-v720/ with these changes:

- Added endpoint for a combined audio+video stream via ffmpeg
- Added a web page with all devices & links
- Added endpoint to send basic commands (flip, ir) via web page
- Removed all features not strictly required for streaming video
- Removed all dependencies not needed for streaming video (particularly open-cv, which doesn't run in alpine)

All credit for reverse engineer this camera's protocol: https://github.com/intx82/a9-v720/
