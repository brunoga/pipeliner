# filesystem-example.star
#
# Scans /tmp for .torrent files, rejects spam, tags and prints the rest.

src      = input("filesystem", path="/tmp", mask="*.torrent")
filtered = process("regexp",    upstream=src, reject=["(?i)spam"])
tagged   = process("set",       upstream=filtered, category="torrent", label="{file_name}")
accepted = process("accept_all", upstream=tagged)
output("print", upstream=accepted, format="[{state}] {title}  ({category})")

pipeline("scan-torrents")
