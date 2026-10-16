# probe

Reports what a release's own container says about itself, read from a **sample of the torrent before the torrent is downloaded**.

A release name is not evidence. `COMPLETE BLURAY FULL-SBS` is a side-by-side re-encode rather than the MVC disc it claims; `2160p` is sometimes a 6 Mbps upscale; `DDP Atmos` is lossy where `TrueHD` is not. The container states the truth, and the part of it that does so is small and predictably placed.

A Blu-ray image keeps its UDF directory at the front and its BDMV metadata — playlists, clip info — at the **end**, with the interleaved stream filling everything between. A Matroska file names its tracks in its first kilobytes. So this plugin fetches the torrent's first and last piece, hands them to the pure-Go container readers in [brunoga/mvc](https://github.com/brunoga/mvc), and stamps what came back.

Measured on a live 40.56 GiB disc: **2 pieces, 27.2 MiB, 0.066% of the torrent, 9.2 seconds** — yielding the exact runtime, the MVC dependent view, the base-view eye, 1920×1080 H.264, TrueHD 7.1 at 8391 kbps and five subtitle tracks.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `timeout` | duration | no | `2m` | Budget for one entry's probe, including fetching its pieces |
| `max_pieces` | int | no | `6` | Most pieces to fetch for one entry. The probe asks for more only when it names bytes it could not read, so this is a ceiling rather than a target |
| `require` | bool | no | `false` | Reject entries whose probe could not run, instead of passing them with `probe_ok` false |
| `parallel` | int | no | `1` | How many entries to probe at once |
| `no_peer_timeout` | duration | no | `30s` | Give up early when no peer has connected and no byte has arrived; `0` disables |
| `listen_port` | int | no | `0` | Port the client listens on and announces. `0` is random, which nothing outside a container can reach. Probe nodes on the same port share one client |
| `cache_ttl` | duration | no | `168h` | How long a **failed** probe is remembered before the swarm is tried again. A container that *was* read is remembered for good. `0` disables the failure cache |
| `cache_only` | bool | no | `false` | Replay stored verdicts and never touch the network. A release nobody has probed comes out with no `probe_*` fields at all |

## Fields set on the entry

All are `MayProduce`: a probe can fail, and a container can decline to state a thing.

| Field | Type | Description |
|-------|------|-------------|
| `probe_ok` | bool | The probe succeeded; the rest are only meaningful when this is true |
| `probe_kind` | string | `disc` or `matroska` |
| `probe_is_3d` | bool | Explicit in both directions — false means the container states it is 2D |
| `probe_3d_layout` | string | `2d`, `mvc`, `sbs`, `tab`, `other` |
| `probe_width` / `probe_height` | int | Coded frame size (a side-by-side 1080p frame is 3840 wide) |
| `probe_upscale` | bool | The coded frame is narrower than the resolution the name claims. Explicit in both directions; absent when either side is unknown |
| `probe_video_codec` | string | `H.264`, `HEVC`, `MVC`, … |
| `probe_duration_sec` | int | Runtime from the container itself, needing no metadata lookup |
| `probe_bitrate_mbps` | float | Torrent size over the container's own duration |
| `probe_audio_codecs` | list | In track order |
| `probe_audio_languages` | list | ISO 639-2, deduped, untagged tracks dropped |
| `probe_subtitle_languages` | list | ISO 639-2 |

### probe_upscale: the resolution claim, checked

`2160p` in a release name is a claim; the coded frame is a fact. `probe_upscale` compares them, so a 1920-wide file calling itself 4K is caught by evidence rather than inferred from its bitrate — which is what [`bitrate`](../filter/bitrate/) was reaching for and cannot do, since a good 4K HEVC encode and a bad one sit at the same number.

The comparison is on **width**, not height. Height varies with the aspect ratio and width does not: a 2160p scope film is commonly 3840×1600, and a height test would read the most cinematic releases there are as upscales. The thresholds (3400 / 1700 / 1100) sit well below each nominal width so a crop or a DCI 4096 frame passes, while the tier below cannot — 1920 is far under what a 2160p claim needs.

A side-by-side 3D frame is twice as wide as its claim (a `1080p` SBS release is 3840 across), which clears the threshold rather than failing it: the test is for a frame too *small* for its claim.

```python
# Refuse a release whose own container contradicts its name.
gate = process("condition", upstream=probed,
               reject='probe_ok and probe_upscale',
               reason="the coded frame is smaller than the resolution claimed")
```

Because `probe` runs below `dedup` in the shipped pipelines, a rejection there discards the alternatives for that run. Pair it with the failure path — mark the release failed so the next run picks the next candidate — rather than leaving the item to come back to the same winner.
| `probe_base_view_right` | bool | A 3D disc says its base view is the right eye, so a conversion must swap views. Discs only |
| `probe_unreachable` | bool | Nothing in the swarm served the sample. Narrower than `probe_ok` false, and a stronger liveness signal than a tracker scrape |
| `probe_pieces` / `probe_bytes` | int | What the probe cost |

## It reports; it does not decide

Every field is a measurement, so one plugin serves a pipeline hunting MVC discs and one hunting 2D films equally. What to do about any of it belongs in a [`condition`](../filter/condition/README.md) rule downstream:

```python
alive = process("torrent_alive", upstream=content, min_seeds=2, verify=True)

# Two pieces per entry, so run this after the cheap name-only gates.
seen  = process("probe", upstream=alive, timeout="3m")

# Now gate on measurement instead of on the release name.
real  = process("condition", upstream=seen,
                reject='probe_ok == true and probe_3d_layout != "mvc"')
```

A 2D pipeline uses the same node and the opposite rule:

```python
flat = process("condition", upstream=seen, reject='probe_is_3d == true')
```

## What it costs

One piece is the smallest range BitTorrent can serve, and on these torrents a piece is 16–32 MiB — so even a 32 KiB Matroska header costs a piece. That is still under a thousandth of a 40 GB disc, but:

- on a **private tracker those bytes count against a ratio**, and each probe is an announce;
- `probe_pieces` and `probe_bytes` record what was spent rather than leaving it to be guessed at;
- entries are probed **one at a time** by default, because pulling tens of megabytes from several swarms at once is the kind of burst a tracker notices. `parallel` raises that when you want it — note the asymmetry: a *failed* probe transfers no bytes, so serialising buys nothing in exactly the case that is slowest;
- nothing reaches the download client and nothing is kept — the sample lives in a temporary directory removed when the run ends.

## Retries are not belt and braces

A disc with hundreds of playlists — Disney titles routinely have them — spills its metadata past the last 16 MiB piece, so assuming two pieces fails on real discs. When the probe cannot read a byte it says which one; the plugin fetches the piece holding it and tries again, up to `max_pieces`. Of ten real discs measured, nine needed two pieces and one needed three; with 32 MiB pieces all ten needed two.

A **parse** failure is final and is not retried. Zero-filled bytes are present bytes, and fetching more of the image will not improve them.

## A dead swarm is not a slow one

The expensive failure is not a slow transfer, it is a swarm that answers nothing at all. A tracker scrape reports seeders that may be long gone, so [`torrent_alive`](../filter/torrent_alive/README.md) can pass a release no peer will serve a byte of — measured on a live run, three such probes burned the full two-minute budget each, six minutes of a six-minute node, while a probe against a healthy swarm finished in **6.4 seconds**.

`no_peer_timeout` ends a probe once nothing has connected *and* nothing has arrived. It is deliberately both conditions: a slow but connected swarm keeps its full budget, because that is the case where waiting pays off — the 93-second probe in that same run was downloading the whole time.

Such an entry gets `probe_unreachable`, which is worth more than `probe_ok` false on its own. `probe_ok` false also covers a parse failure or a missing `.torrent`; `probe_unreachable` says something narrower and more useful — **the full download would not have gone any better either**. That is a liveness verdict a scrape cannot give you, and a condition can act on it:

```python
alive = process("condition", upstream=seen, reject="probe_unreachable == true")
```

## Reachability

A peer connection is opened by one side, and a seeder behind a home router cannot be dialled: it can only take part by dialling us. With `listen_port=0` inside Docker the client listens on a random port the host never forwards, so every such seeder is invisible and a live swarm can read as `probe_unreachable`. Publish a fixed port on the host for both TCP and UDP (uTP shares it), with the host port equal to the container port because the client announces its own number, and set `listen_port` on every probe node.

Every node with the same `listen_port` shares one client, reference-counted and closed by the last node's `Shutdown`. That is not an optimisation: a config has a probe node per lane and two clients cannot bind one port, and on a reload the new config's nodes are built while the old ones still hold it.

A failed probe logs what the swarm did — `tracker` (each announce URL's last result by host: `N peers`, the tracker's error, or `never`), `peers_known`, `peers_connected`, `peers_unchoked`, `useful_bytes` — so a refused announce, an empty swarm and peers that would not send are told apart. The tracker result is read from the library's status text, since it exposes no API for an HTTP tracker's announce result; a test runs the real client against a fake tracker so a format change fails CI instead of turning the field into `unknown`. Hosts only: an announce URL carries the passkey.

## What a container does not state

Fields are left unset rather than written as zero, so a condition can tell "it says 0" from "it did not say".

- **Video bit depth** is never stated by a disc's clip info.
- **A disc's audio channel count and bitrate** come from the stream's opening frames, which lie in the first piece on only some discs. Decide on codec and language, which are always present.
- **Brazilian Portuguese** cannot be distinguished: Blu-ray language codes are ISO 639-2, so it reads as `por`.
- A probe that **cannot run** leaves `probe_ok` false and the entry otherwise untouched, because a release should not be lost to an unreachable swarm. `require=True` rejects those instead.

## Verdicts are remembered

Every probe is recorded in `cache_probe`, keyed by **lowercase info-hash** — the content, not the release, so two indexers posting the same torrent share one verdict and so do two pipelines.

The two outcomes have different lifetimes because they are different kinds of fact:

- **A container that was read** is a property of the bytes, and the info-hash pins the bytes. That answer can never change, so it is kept for good.
- **A probe that failed** is a property of the swarm at a moment — nobody was serving, or nobody served fast enough. Swarms recover, so that one expires after `cache_ttl`.

A **dry run keeps a success and discards a failure**, because the two differ in whether they can go stale. A container that was read is a fact about immutable bytes, so recording it from a dry run can never be wrong and saves re-pulling 20-25 MiB. A failure is a snapshot of a swarm, and recording one would keep a gate refusing that release on the next *real* run for a whole `cache_ttl` on the strength of a diagnostic.

The whole `mvcprobe.Result` is stored and replayed through the same function a live probe uses, so a replayed entry and a freshly probed one cannot drift. It also keeps the two fields derived from the *entry* rather than the container correct for a release whose name differs from the one probed: `probe_upscale` compares the frame against the resolution the **name** claims, and `probe_bitrate_mbps` divides the **torrent's** size by the container's duration. Both are recomputed on replay.

### Gating above a collapse

`cache_only=True` is the point of all this. A probe is expensive, so it belongs below [`limit`](../filter/limit/README.md), where it runs on the few releases about to be downloaded — but a node that can refuse a release must run **above** [`dedup`](../filter/dedup/README.md), or the alternatives are already gone when the refusal lands (see the user guide's node-ordering rule). Those two pull in opposite directions.

A `cache_only` node resolves it. Put one above `dedup` and it replays what earlier runs learned, for free, so the refusal happens while the alternatives are still there; the real probe stays below `limit`:

```python
# Free: replays verdicts earlier runs paid for, touches no network.
known = process("probe", upstream=alive, cache_only=True)
# A release nobody has probed carries no probe_ok, and an absent field
# compares false — so this refuses the known-bad and passes the unknown.
good  = process("condition", upstream=known,
                reject='probe_ok == false',
                reason="an earlier run could not read this release's container")
pick  = process("dedup", upstream=good)
few   = process("limit", upstream=pick, n=3)
# Costs megabytes, so it runs on the few: these are new to the cache.
told  = process("probe", upstream=few, timeout="2m")
```

What this buys: a release refused on its own merits is not chosen again, so the next run's `dedup` picks the **next** alternative and the backlog drains. Without it the same release wins every night, is refused every night, and the item never arrives — measured on the live `3d-mvc` ambiguous lane, which spent its whole nightly budget re-probing one release whose swarm would not serve.

## Placement
## Placement

`probe` requires `torrent_info_hash`, so it sits after [`metainfo_torrent`](../metainfo/torrent/README.md). Put it after every gate that decides from the release name — [`quality`](../filter/quality/README.md), [`trailer`](../filter/trailer/README.md), the trackers, [`torrent_alive`](../filter/torrent_alive/README.md) — because those cost nothing and this costs megabytes. `torrent_alive` earns its place first in particular: a probe needs a seeder, and that filter has already established there is one.

## DAG role

| Property | Value |
|---|---|
| Role | processor |
| Refusal | per-release with `require=true`, none without it |
| Requires | `torrent_info_hash` |
| MayProduce | the `probe_*` fields above |
