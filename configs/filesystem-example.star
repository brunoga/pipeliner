# filesystem-example.star
#
# Scans /tmp for .torrent files, rejects spam, tags and prints the rest.

task("scan-torrents", [
    plugin("filesystem", path="/tmp", mask="*.torrent"),
    plugin("regexp", reject=["(?i)spam"]),
    plugin("set", category="torrent", label="{filename}"),
    plugin("print", format="[{state}] {title}  ({category})"),
])
