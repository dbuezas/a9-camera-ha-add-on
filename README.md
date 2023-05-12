# a9 add on

## Instructions

Until I make give this the correct structure to be installed in a standard way, the steps are: 0. Get the camera to connect to your access point (with the app or following instructions in https://github.com/intx82/a9-v720) 0. Reroute \*.naxclow.com to your HA computer IP (e.g using the AdGuard addon and configuring your router to use that as DNS provider)

1. Download all the code in here and put it inside the `~/addons/a9-v720`
2. [![Open your Home Assistant instance and show the Supervisor add-on store.](https://my.home-assistant.io/badges/supervisor_store.svg)](https://my.home-assistant.io/redirect/supervisor_store/)
3. Click on the three dots overflow menu on the top right, then `Check for updates`
4. There should now be a "A9 Fake camera server" addon.
5. Install and start it.
6. Go to logs, grab the ID of your cameras (leave it running for half an hour if you have cameras with new FW version)
7. Learn & Install the Go2rpc addon, and WebRTC custom card
8. in go2rtc.yaml, add:

```yaml
streams:
  v9_camera: ffmpeg:http://127.0.0.1:80/dev/[your-cam_id]/stream#video=h264#audio=copy
```

9. I didn't realise how complex this was. But you are done! you can use `v9_camera` in your WebRTC cards now.

## ToDo:

- [x] Implement audio streamiming from STA mode
- [x] Create an endpoint with merged video and audio with ffmpeg and named pipes
- [x] Remove OpenCV requirement so the server can run in an Alpine docker base
- [x] Find the correct way to configure ffmpeg to interprete the raw streams correctly
- [ ] Expose the commands to toggle IR mode and other options via UDP as entities
- [ ] Expose entities containing the status of the camera as fetched via UDP
- [ ] Make the structure of this repo compliant so it can be installed more easily
- [ ] Find out how to get the low delay of opuslib without getting broken audio.

## Credits & details

This addon https://github.com/dbuezas/a9-camera-ha-add-on

Python code derived from https://github.com/intx82/a9-v720/ with these changes:

- Added endpoint for a combined audio+video stream via ffmpeg
- Removed all features not strictly required for streaming video
- Removed all dependencies not needed for streaming video (particularly open-cv, which doesn't run in alpine)

All credit for reverse engineer this camera's protocol: https://github.com/intx82/a9-v720/
