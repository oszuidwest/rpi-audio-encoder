# rpi-audio-encoder
This repository contains the audio streaming software for [ZuidWest FM](https://www.zuidwestfm.nl/) in the Netherlands. It uses a Rapsberry Pi 4 and a [HiFiBerry Digi+ I/O](https://www.hifiberry.com/shop/boards/hifiberry-digi-io/) as audio input. As encoder ffmpeg is used, which is combined with Supervisor to manage the process via a webinterface. It sends audio to an Icecast2 or SRT server.

This encoder resides in the studio and is connected to the digital output of an Orban Optimod. It can stream to any Icecast or SRT server. Our server software to complete the audio stack can be found in [this respository](https://github.com/oszuidwest/liquidsoap-ubuntu).

<img src="https://user-images.githubusercontent.com/6742496/221062672-7a073a71-3aa3-40c2-bf2f-e46a3988b0b4.png" width=60% height=60%>

# How to prepare the Rapsberry Pi
- Install Ubuntu Server 22.04 LTS 64-bit 
- Follow the guide on https://www.hifiberry.com/docs/software/configuring-linux-3-18-x/ to set-up the HiFiBerry
- Ensure you are root by running `sudo su`
- Download and run the install script with the command `/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/oszuidwest/rpi-encoder/main/install.sh)"`

⚠️ There is a [major problem with kernel verions 6.x and recording from a HifiBerry with FFmpeg](https://github.com/raspberrypi/linux/issues/5709). Linux versions that ship with kernel 6.0 or newer do not work. We strongly recommend using Ubuntu 22.04 LTS with kernel 5.15. Raspbian 11 or 12 ship kernel 6.x and do not work. ⚠️ 

# How to configure the audio processor
- Connect the digital output of the audio processor to the input of the HiFiBerry.
- Ensure the processor is sending out 48khz 16-bits audio. The HiFiBerry can't resample. This is hardcoded.
- If possible, configure the digital output to send SPDIF data. AES/EBU could work, but is not 100% the same standard.

_This is an example for an Orban Optimod:_

<img src="https://user-images.githubusercontent.com/6742496/210573724-966064f9-e8b9-4d28-a40c-29385b20daab.png" width=50% height=50%>

# Audio encoding presets
These audio encoding presets are inclued. They are limited to what Icecast supports:
- `mp2` sends MPEG-1 Audio Layer II audio on 384 kbit/s. This is considered the gold standard for compressed broadcast audio.
- `mp3` sends MPEG-1 Audio Layer III audio on 320 kbit/s. This is the highest quality mp3 possible.
- `ogg/vorbis` sends OGG Vorbis audio on 500 kbit/s. This is the highest quality ogg/vorbis possible.
- `ogg/flac` sends FLAC audio in an OGG wrapper on ~1200 kbit/s. This is the highest possible uncompressed audio.

### SRT support
Besides Icecast we also support SRT for streaming. It's stable but we didn't do an endurance test yet. A working SRT implementation of the server software [can be found here](https://github.com/oszuidwest/liquidsoap-ubuntu/). In the future Icecast support might be deprecated, but we are giving SRT a bit more time to prove itself.

More information about SRT:
- SRT overview: https://datatracker.ietf.org/meeting/107/materials/slides-107-dispatch-srt-overview-01
- SRT deployment guide: https://www.vmix.com/download/srt_alliance_deployment_guide.pdf
- SRT 101 video: https://www.youtube.com/watch?v=e5YLItNG3lA 
