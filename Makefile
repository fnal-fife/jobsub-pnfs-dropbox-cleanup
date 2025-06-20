NAME = jobsub-pnfs-dropbox-cleanup
VERSION = v0.1.0
ROOTDIR = $(shell pwd)
BUILD = $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
rpmVersion := $(subst v,,$(VERSION))
buildTarName = $(NAME)-$(rpmVersion)
buildTarPath = $(ROOTDIR)/$(buildTarName).tar.gz
SOURCEDIR = $(ROOTDIR)/$(buildTarName)
executables = $(NAME)
specfile := $(ROOTDIR)/packaging/$(NAME).spec
ifdef RACE
raceflag := -race
else
raceflag :=
endif

all: build tarball spec rpm
.PHONY: all clean clean-all build tarball spec rpm

rpm: rpmSourcesDir := $$HOME/rpmbuild/SOURCES
rpm: rpmSpecsDir := $$HOME/rpmbuild/SPECS
rpm: rpmDir := $$HOME/rpmbuild/RPMS/x86_64/
rpm: spec tarball
	cp $(specfile) $(rpmSpecsDir)/
	cp $(buildTarPath) $(rpmSourcesDir)/
	cd $(rpmSpecsDir); \
	rpmbuild -ba ${NAME}.spec
	find $$HOME/rpmbuild/RPMS -type f -name "$(NAME)-$(rpmVersion)*.rpm" -cmin 1 -exec cp {} $(ROOTDIR)/ \;
	echo "Created RPM and copied it to current working directory"


spec:
	sed -Ei 's/Version\:[ ]*.+/Version:        $(rpmVersion)/' $(specfile)
	echo "Set version in spec file to $(rpmVersion)"


tarball: build
	mkdir -p $(SOURCEDIR)
	cp $(ROOTDIR)/$(NAME) $(SOURCEDIR)  # Executables
	cp $(ROOTDIR)/$(NAME).yml $(ROOTDIR)/packaging/$(NAME).logrotate $(ROOTDIR)/packaging/$(NAME).cron $(SOURCEDIR)  # Config files
	tar -czf $(buildTarPath) -C $(ROOTDIR) $(buildTarName)
	echo "Built deployment tarball"


build:
	echo "Building executable"; \
	go build $(raceflag) -ldflags="-X main.buildTimestamp=$(BUILD) -X main.version=$(VERSION)";  \
	echo "Built $(NAME)"; \


clean-all: clean
	(test -e $(ROOTDIR)/$(NAME)-$(rpmVersion)*.rpm) && (rm $(ROOTDIR)/$(NAME)-$(rpmVersion)*.rpm)


clean:
	(test -e $(buildTarPath)) && (rm $(buildTarPath))
	(test -e $(SOURCEDIR)) && (rm -Rf $(SOURCEDIR))