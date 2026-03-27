# Create manpages. If installed for a particular user, these can be used
# with `zelta help` by setting the path via ZELTA_DOC, e.g.,
# `export ZELTA_DOC="${HOME}/.config/zelta/doc"`

SRC      = $(wildcard *.md)
MAN7     = $(patsubst %.7.md,%.7,$(filter %.7.md,$(SRC)))
MAN8     = $(patsubst %.md,%.8,$(filter-out %.7.md,$(SRC)))

all: $(MAN7) $(MAN8)

# Specific rule for Section 7 (Misc/Overview)
%.7: %.7.md
	pandoc --standalone --to man $< -o $@

# Catch-all for Section 8 (Admin commands)
%.8: %.md
	pandoc --standalone --to man $< -o $@

clean:
	rm -f $(MAN7) $(MAN8)

.PHONY: all clean
