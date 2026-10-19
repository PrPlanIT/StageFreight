// apt 3.0.3 apt-private/private-cmndline.cc (https://salsa.debian.org/apt-team/apt/-/blob/3.0.3/apt-private/private-cmndline.cc):
// the option blocks apt-get install and apt install register: lines 181-202, 266-298, 375 and 413-423.
   if (CmdMatches("install", "reinstall", "remove", "purge", "upgrade", "dist-upgrade",
	    "dselect-upgrade", "autoremove", "autopurge", "full-upgrade"))
   {
      addArg(0, "show-progress", "DpkgPM::Progress", 0);
      addArg('f', "fix-broken", "APT::Get::Fix-Broken", 0);
      addArg(0, "purge", "APT::Get::Purge", 0);
      addArg('V',"verbose-versions", "APT::Get::Show-Versions",0);
      addArg(0, "list-columns", "APT::Get::List-Columns", 0);
      addArg(0, "autoremove", "APT::Get::AutomaticRemove", 0);
      addArg(0, "auto-remove", "APT::Get::AutomaticRemove", 0);
      addArg(0, "reinstall", "APT::Get::ReInstall", 0);
      addArg(0, "solver", "APT::Solver", CommandLine::HasArg);
      addArg(0, "strict-pinning", "APT::Solver::Strict-Pinning", 0);
      addArg(0, "planner", "APT::Planner", CommandLine::HasArg);
      addArg(0, "comment", "APT::History::Comment", CommandLine::HasArg);
      addArg('U', "update", "APT::Update", 0);
      if (CmdMatches("upgrade"))
      {
         addArg(0, "new-pkgs", "APT::Get::Upgrade-Allow-New", 
                CommandLine::Boolean);
      }
   }
   bool const found_something = Args.empty() == false;

   // FIXME: move to the correct command(s)
   addArg('d',"download-only","APT::Get::Download-Only",0);
   addArg('y',"yes","APT::Get::Assume-Yes",0);
   addArg('y',"assume-yes","APT::Get::Assume-Yes",0);
   addArg(0,"assume-no","APT::Get::Assume-No",0);
   addArg('u',"show-upgraded","APT::Get::Show-Upgraded",0);
   addArg('m',"ignore-missing","APT::Get::Fix-Missing",0);
   addArg('t',"target-release","APT::Default-Release",CommandLine::HasArg);
   addArg('t',"default-release","APT::Default-Release",CommandLine::HasArg);
   addArg('S', "snapshot", "APT::Snapshot", CommandLine::HasArg);
   addArg(0,"download","APT::Get::Download",0);
   addArg(0,"fix-missing","APT::Get::Fix-Missing",0);
   addArg(0,"ignore-hold","APT::Ignore-Hold",0);
   addArg(0,"upgrade","APT::Get::upgrade",0);
   addArg(0,"only-upgrade","APT::Get::Only-Upgrade",0);
   addArg(0,"allow-change-held-packages","APT::Get::allow-change-held-packages",CommandLine::Boolean);
   addArg(0,"allow-remove-essential","APT::Get::allow-remove-essential",CommandLine::Boolean);
   addArg(0,"allow-downgrades","APT::Get::allow-downgrades",CommandLine::Boolean);
   addArg(0,"force-yes","APT::Get::force-yes",0);
   addArg(0,"print-uris","APT::Get::Print-URIs",0);
   addArg(0,"trivial-only","APT::Get::Trivial-Only",0);
   addArg(0,"mark-auto","APT::Get::Mark-Auto",0);
   addArg(0,"remove","APT::Get::Remove",0);
   addArg(0,"only-source","APT::Get::Only-Source",0);
   addArg(0,"allow-unauthenticated","APT::Get::AllowUnauthenticated",0);
   addArg(0,"install-recommends","APT::Install-Recommends",CommandLine::Boolean);
   addArg(0,"install-suggests","APT::Install-Suggests",CommandLine::Boolean);
   addArg(0,"fix-policy","APT::Get::Fix-Policy-Broken",0);
   addArg(0, "with-source", "APT::Sources::With::", CommandLine::HasArg);

   return found_something;
   addArg(0, "with-source", "APT::Sources::With::", CommandLine::HasArg);
   // options without a command
   addArg('h', "help", "help", 0);
   addArg('v', "version", "version", 0);
   // general options
   addArg(0, "color", "APT::Color", 0);
   addArg('q', "quiet", "quiet", CommandLine::IntLevel);
   addArg(0, "audit", "APT::Audit", 0);
   addArg('q', "silent", "quiet", CommandLine::IntLevel);
   addArg('c', "config-file", 0, CommandLine::ConfigFile);
   addArg('o', "option", 0, CommandLine::ArbItem);
   addArg(0, NULL, NULL, 0);
