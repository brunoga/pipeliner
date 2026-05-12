# filesystem-cleanup.star
#
# Deletes all *.part files under /tmp/downloads and logs each removal.

src     = input("filesystem", path="/tmp/downloads", recursive=True, mask="*.part")
accepted = process("accept_all", upstream=src)
output("exec",  upstream=accepted, command="rm -f {file_location}")
output("print", upstream=accepted, format="removed: {title}")

pipeline("cleanup-old-downloads")
