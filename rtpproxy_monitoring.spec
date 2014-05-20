#debuginfo not supported with Go
%global debug_package %{nil}


Name:           rtpproxy_monitoring
Version:        1.1
Release:        1%{?dist}
Summary:        RTPproxy monitoring application
Group:          System Environment/Daemons
License:        BSD
URL:            https://github.com/lemenkov/rtpproxy_monitoring
Source0:        https://github.com/lemenkov/rtpproxy_monitoring/archive/%{version}/%{name}-%{version}.tar.gz
BuildRequires:  golang
BuildRequires:  git
%if 0%{?el7}%{?fedora}
BuildRequires:  systemd
Requires(post): systemd
Requires(preun): systemd
Requires(postun): systemd
%endif


%description
%{summary}.


%prep
%setup -q


%build
CFLAGS="$RPM_OPT_FLAGS" make


%install
install -p -m 0755 -D %{name} $RPM_BUILD_ROOT%{_bindir}/%{name}
install -p -m 0644 -D %{name}.sysconfig $RPM_BUILD_ROOT%{_sysconfdir}/sysconfig/%{name}
%if 0%{?el7}%{?fedora}
# install systemd service
install -D -m 0644 -p %{name}.service %{buildroot}%{_unitdir}/%{name}.service
%else
# install Upstart service
install -p -m 0644 -D %{name}.upstart $RPM_BUILD_ROOT%{_sysconfdir}/init/%{name}.conf
%endif


%post
%if 0%{?el7}%{?fedora}
%systemd_post %{name}.service
%endif


%preun
%if 0%{?el7}%{?fedora}
%systemd_preun %{name}.service
%endif


%files
%doc
%config(noreplace) %{_sysconfdir}/sysconfig/%{name}
%if 0%{?el7}%{?fedora}
%{_unitdir}/%{name}.service
%else
%{_sysconfdir}/init/%{name}.conf
%endif
%{_bindir}/%{name}


%changelog
