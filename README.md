# rpi-audio-encoder
This repository contains the audio streaming software for [ZuidWest FM](https://www.zuidwestfm.nl/) in the Netherlands. The setup involves a Raspberry Pi 4 or 5 and a [HiFiBerry Digi+ I/O](https://www.hifiberry.com/shop/boards/hifiberry-digi-io/) for audio input. The system uses FFmpeg as an encoder, integrated with Supervisor for process management via a web interface. It supports audio streaming to a SRT server.

The encoder, stationed in the studio, connects to the digital output of an Orban Optimod, enabling streaming to any SRT server. Companion server software to complete the audio stack is available in [this repository](https://github.com/oszuidwest/liquidsoap-ubuntu).

<img src="https://user-images.githubusercontent.com/6742496/221062672-7a073a71-3aa3-40c2-bf2f-e46a3988b0b4.png" width=60% height=60%>

# Preparing the Raspberry Pi
- Install Ubuntu Server 22.04 LTS 64-bit or Raspbian 12 64-bit.
- Follow the guide at https://www.hifiberry.com/docs/software/configuring-linux-3-18-x/ for HiFiBerry setup.
- Gain root access with `sudo su`.
- Download and execute the install script using `/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/oszuidwest/rpi-encoder/main/install.sh)"`.

⚠️ Note: A [significant issue exists with kernel versions 6.0 - 6.5 when recording from a HiFiBerry using FFmpeg](https://github.com/raspberrypi/linux/issues/5709). Linux distributions with kernel 6.0 to 6.5 are incompatible. We highly recommend Ubuntu 22.04 Server LTS with kernel 5.15 or Raspbian 12 with kernel 6.6 or newer.

# Post installation clean-up
- You probably don't need WiFi. Disable it by adding `dtoverlay=disable-wifi` to `/boot/firmware/config.txt`
- You probably don't need tools for Thunderbolt, Bluetooth, NTFS, Remote Syslogs and Telnet. Remove them with `apt remove bolt bluez ntfs-3g rsyslog telnet`

On Ubuntu 22.04:
- You probably don't need LXD for managing containerized applications and virtual machines. Remove it with `snap remove lxd`
- You can speed-up booting by removing `optional: true` from eth0 in `/etc/netplan/50-cloud-init.yaml`.

# Configuring the Audio Processor
- Connect the digital output of the audio processor to the HiFiBerry's input.
- Ensure the processor outputs 48kHz 16-bit audio, as the HiFiBerry does not support resampling. This setting is hardcoded.
- Preferably, set the digital output to transmit SPDIF data. Although AES/EBU might work, it is not identically standardized.

_Example for an Orban Optimod:_

<img src="https://user-images.githubusercontent.com/6742496/210573724-966064f9-e8b9-4d28-a40c-29385b20daab.png" width=50% height=50%>

# Audio Encoding Presets
Included audio encoding presets:
- `mp2`: Streams MPEG-1 Audio Layer II audio at 384 kbit/s, regarded as the benchmark for compressed broadcast audio.
- `mp3`: Streams MPEG-1 Audio Layer III audio at 320 kbit/s, the highest mp3 quality achievable.
- `ogg`: Streams OGG Vorbis audio at 500 kbit/s, the highest quality for ogg/vorbis.
- `wav`: Streams uncompressed 16-bit Little Endian audio, the pinnacle of uncompressed audio quality.

### Icecast Support
Icecast support was removed in version 2.0. SRT has been thoroughly evaluated for reliability. [Version 1.1](https://github.com/oszuidwest/rpi-audio-encoder/releases/tag/1.1.0) with support for Icecast is still available for download.

Additional information on SRT:
- SRT overview: https://datatracker.ietf.org/meeting/107/materials/slides-107-dispatch-srt-overview-01
- SRT deployment guide: https://www.vmix.com/download/srt_alliance_deployment_guide.pdf
- SRT 101 video: https://www.youtube.com/watch?v=e5YLItNG3lA
