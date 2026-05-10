# filesystem-cleanup.star
#
# Deletes all *.part files under /tmp/downloads and logs each removal.

task("cleanup-old-downloads", [
    plugin("filesystem", path="/tmp/downloads", recursive=True, mask="*.part"),
    plugin("regexp", accept=[".+"]),
    plugin("exec", command="rm -f {file_location}"),
    plugin("print", format="removed: {title}"),
])
