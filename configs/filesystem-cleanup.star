# filesystem-cleanup.star
#
# Deletes all *.part files under /tmp/downloads and logs each removal.

src     = input("filesystem", path="/tmp/downloads", recursive=True, mask="*.part")
accepted = process("accept_all", from_=src)
output("exec",  from_=accepted, command="rm -f {file_location}")
output("print", from_=accepted, format="removed: {title}")

pipeline("cleanup-old-downloads")
