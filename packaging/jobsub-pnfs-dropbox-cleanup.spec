Name:           jobsub-pnfs-dropbox-cleanup
Version:        0.1.4
Release:        1%{?dist}
Summary:        Cleanup script for resilient dCache

Group:          Applications/System
License:        Fermitools Software Legal Information (Modified BSD License)
Source0:        %{name}-%{version}.tar.gz

%global debug_package %{nil}

BuildRoot:      %(mktemp -ud %{_tmppath}/%{name}-%{version}-XXXXXX)
BuildArch:      x86_64

Requires:       krb5-workstation
Requires:       condor
Requires:       htgettoken
Requires:       gfal2-util-scripts


%description
This package provides an executable to clean up resilient dCache files that are no longer needed

%prep
test ! -d %{buildroot} || {
rm -rf %{buildroot}
}

%setup -q

%build

%install

# Config file to /etc/jobsub-pnfs-dropbox-cleanup
mkdir -p %{buildroot}/%{_sysconfdir}/%{name}
install -m 0774 %{name}.yml %{buildroot}/%{_sysconfdir}/%{name}/%{name}.yml

# Executables to /usr/bin
mkdir -p %{buildroot}/%{_bindir}
install -m 0755 %{name}  %{buildroot}/%{_bindir}/%{name}

# Cron and logrotate
mkdir -p %{buildroot}/%{_sysconfdir}/cron.d
install -m 0644 %{name}.cron %{buildroot}/%{_sysconfdir}/cron.d/%{name}
mkdir -p %{buildroot}/%{_sysconfdir}/logrotate.d
install -m 0644 %{name}.logrotate %{buildroot}/%{_sysconfdir}/logrotate.d/%{name}


%clean
rm -rf %{buildroot}

%files
%defattr(0755, rexbatch, fife, 0774)
%{_sysconfdir}/%{name}
%config(noreplace) %{_sysconfdir}/%{name}/%{name}.yml
%config(noreplace) %attr(0644, root, root) %{_sysconfdir}/cron.d/%{name}
%config(noreplace) %attr(0644, root, root) %{_sysconfdir}/logrotate.d/%{name}
%{_bindir}/%{name}

%post
# Set owner of /etc/jobsub-pnfs-dropbox-cleanup
test -d %{_sysconfdir}/%{name} && {
chown rexbatch:fife %{_sysconfdir}/%{name}
}

# Logfiles at /var/log/jobsub-pnfs-dropbox-cleanup
test -d /var/log/%{name} || {
install -d /var/log/%{name} -m 0774 -o rexbatch -g fife
}


%changelog
* Wed Jun 18 2025 Shreyas Bhat <sbhat@fnal.gov> - 0.1.0
- First version of RPM
