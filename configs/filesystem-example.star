# filesystem-example.star
#
# Scans /tmp for .torrent files, rejects spam, tags and prints the rest.

src      = input("filesystem", path="/tmp", mask="*.torrent")
filtered = process("regexp",    from_=src, reject=["(?i)spam"])
tagged   = process("set",       from_=filtered, category="torrent", label="{file_name}")
accepted = process("accept_all", from_=tagged)
output("print", from_=accepted, format="[{state}] {title}  ({category})")

pipeline("scan-torrents")
