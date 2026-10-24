# metainfo_3d

Marks whether a movie was released in 3D by its studio, shot in 3D or converted to 3D for release. A 3D copy of a film without a studio 3D version was made by a fan from the 2D film, however the release is labelled. Fan-made Blu-ray 3D images exist and probe as genuine MVC, so neither the name nor the container can tell; the film's history can.

The answer comes from Wikipedia's *List of 3D films (1914–2004)* and *List of 3D films (2005–present)*: every film shown in 3D in cinemas, including studio re-releases (*Titanic*, 2012) and conversions that never reached disc (*Mulan*, 2020). Studio 3D versions made only for streaming are not listed and read as `false`.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `refresh` | duration | no | `168h` | How long the downloaded list is used before it is fetched again |

## Fields set on entry

| Field | Type | Description |
|-------|------|-------------|
| `movie_3d_official` | bool | The studio released the film in 3D. `false` means a 3D copy is a fan conversion. Unset when no list has ever been read |
| `movie_3d_native` | bool | The film was shot in 3D rather than converted. Set only on listed films |

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| MayProduce | `movie_3d_official`, `movie_3d_native` |
| Requires | — |

## Matching

A film matches when one of its titles names a listed film released within a year of `video_year`. Titles tried: TMDb's title, original title and alternative titles (if `metainfo_tmdb` ran upstream), the movies tracker's match, and the title parsed from the release name, also with any "Episode N" removed. A listed film's years are its 3D release, its original release for a re-release, and the year in its article name, so remakes stay apart (*Mulan* 2020 is listed, *Mulan* 1998 is not).

## Failure handling

The parsed list is stored in `cache_metainfo_3d` and fetched again after `refresh` (two requests a week by default). Films are only ever added to these lists, so a refetch is merged into the stored list rather than replacing it: a vandalised or restructured page can never make a known film read as a fan conversion. A failed refetch keeps using the stored list. When no list has ever been read, no field is set, so a `movie_3d_official != true` gate refuses everything instead of letting conversions through. A fetched page with no films (a redirect, a layout change) counts as a failure rather than an empty list.

## Example

```python
studio = process("metainfo_3d", upstream=meta)
real   = process("condition", upstream=studio,
                 reject="movie_3d_official != true",
                 reason="no studio 3D version: a fan conversion")
```
