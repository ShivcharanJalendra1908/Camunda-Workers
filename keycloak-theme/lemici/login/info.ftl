<#import "template.ftl" as layout>

<@layout.registrationLayout displayMessage=false; section>
    <#if section = "header">
        <div class="login-header">
            <h1>Status Update</h1>
        </div>
    <#elseif section = "form">
        <div id="kc-info-message" style="text-align: center; padding: 20px 0;">
            <script>
                function handleReturnToLogin() {
                    var host = window.location.hostname;
                    if (host.indexOf('dev') !== -1 && host.indexOf('lemici.com') !== -1) {
                        window.location.href = 'https://dev.lemici.com/login';
                    } else if (host.indexOf('lemici.com') !== -1) {
                        window.location.href = 'https://www.lemici.com/login';
                    } else if (host === 'localhost' || host === '127.0.0.1' || host.indexOf('192.168.') === 0) {
                        window.location.href = 'http://localhost:3000/login';
                    } else {
                        window.location.href = '${url.loginUrl}';
                    }
                }
                function handleReturnToApp() {
                    var host = window.location.hostname;
                    if (host.indexOf('dev') !== -1 && host.indexOf('lemici.com') !== -1) {
                        window.location.href = 'https://dev.lemici.com/';
                    } else if (host.indexOf('lemici.com') !== -1) {
                        window.location.href = 'https://www.lemici.com/';
                    } else if (host === 'localhost' || host === '127.0.0.1' || host.indexOf('192.168.') === 0) {
                        window.location.href = 'http://localhost:3000/';
                    } else {
                        window.location.href = '/';
                    }
                }
            </script>
            <#-- Icon based on message type -->
            <#if message.type = "success">
                <div style="width: 64px; height: 64px; background: rgba(22, 163, 74, 0.1); color: #16a34a; border-radius: 50%; display: flex; align-items: center; justify-content: center; margin: 0 auto 20px auto;">
                    <svg fill="none" viewBox="0 0 24 24" stroke-width="2.5" stroke="currentColor" style="width: 32px; height: 32px;"><path stroke-linecap="round" stroke-linejoin="round" d="M4.5 12.75l6 6 9-13.5" /></svg>
                </div>
            <#elseif message.type = "warning">
                <div style="width: 64px; height: 64px; background: rgba(217, 119, 6, 0.1); color: #d97706; border-radius: 50%; display: flex; align-items: center; justify-content: center; margin: 0 auto 20px auto;">
                    <svg fill="none" viewBox="0 0 24 24" stroke-width="2.5" stroke="currentColor" style="width: 32px; height: 32px;"><path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" /></svg>
                </div>
            <#elseif message.type = "error">
                <div style="width: 64px; height: 64px; background: rgba(220, 38, 38, 0.1); color: #dc2626; border-radius: 50%; display: flex; align-items: center; justify-content: center; margin: 0 auto 20px auto;">
                    <svg fill="none" viewBox="0 0 24 24" stroke-width="2.5" stroke="currentColor" style="width: 32px; height: 32px;"><path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" /></svg>
                </div>
            <#else>
                <div style="width: 64px; height: 64px; background: rgba(37, 99, 235, 0.1); color: #2563eb; border-radius: 50%; display: flex; align-items: center; justify-content: center; margin: 0 auto 20px auto;">
                    <svg fill="none" viewBox="0 0 24 24" stroke-width="2.5" stroke="currentColor" style="width: 32px; height: 32px;"><path stroke-linecap="round" stroke-linejoin="round" d="M11.25 11.25l.041-.02a.75.75 0 111.063.854l-.512 1.453-.041.02a.75.75 0 01-1.063-.854l.512-1.453zM12 7.5a.75.75 0 110-1.5.75.75 0 010 1.5zM21 12a9 9 0 11-18 0 9 9 0 0118 0z" /></svg>
                </div>
            </#if>

            <p style="font-size: 16px; font-weight: 500; color: #374151; margin-bottom: 24px; line-height: 1.6;">
                ${kcSanitize(message.summary)?no_esc}
            </p>

            <#-- Redirect Info or Countdown -->
            <#if message.summary?contains("verified") || message.summary?contains("activation")>
                <#-- Email Verified Success page: Notify original tab (via localStorage) -->
                <script>
                    (function() {
                        localStorage.setItem('email_verified_success', 'true');
                    })();
                </script>
                <p style="font-size: 14px; color: #6b7280; margin-bottom: 24px; line-height: 1.5;">
                    Redirecting to application in <span id="countdown-sec" style="font-weight: 600; color: #6D3E93;">10</span> seconds...
                </p>
                <#if pageRedirectUri?has_content>
                    <a href="${pageRedirectUri}" class="pf-c-button pf-m-primary" style="text-decoration: none; display: inline-block; padding: 10px 24px; background: #6D3E93; color: white; border-radius: 8px; font-weight: 600; font-size: 15px;">
                        ${kcSanitize(msg("backToApplication"))?no_esc}
                    </a>
                <#else>
                    <a href="${url.loginUrl}" onclick="event.preventDefault(); handleReturnToApp();" class="pf-c-button pf-m-primary" style="text-decoration: none; display: inline-block; padding: 10px 24px; background: #6D3E93; color: white; border-radius: 8px; font-weight: 600; font-size: 15px;">
                        ${kcSanitize(msg("backToApplication"))?no_esc}
                    </a>
                </#if>
                <script>
                    (function() {
                        var sec = 10;
                        var timer = setInterval(function() {
                            sec--;
                            var el = document.getElementById('countdown-sec');
                            if (el) el.innerText = sec;
                            if (sec <= 0) {
                                clearInterval(timer);
                                handleReturnToApp();
                            }
                        }, 1000);
                    })();
                </script>
            <#elseif message.summary?contains("receive") || message.summary?contains("instruction") || message.summary?contains("sent")>
                <#-- Email Sent page: Listen for success event from other tabs (via localStorage) -->
                <#if pageRedirectUri?has_content>
                    <a href="${pageRedirectUri}" class="pf-c-button pf-m-primary" style="text-decoration: none; display: inline-block; padding: 10px 24px; background: #6D3E93; color: white; border-radius: 8px; font-weight: 600; font-size: 15px;">
                        ${kcSanitize(msg("backToApplication"))?no_esc}
                    </a>
                <#else>
                    <a href="${url.loginUrl}" onclick="event.preventDefault(); handleReturnToLogin();" class="pf-c-button pf-m-primary" style="text-decoration: none; display: inline-block; padding: 10px 24px; background: #6D3E93; color: white; border-radius: 8px; font-weight: 600; font-size: 15px;">
                        ${kcSanitize(msg("backToLogin"))?no_esc}
                    </a>
                </#if>
                <script>
                    (function() {
                        // Clear any old success flag first
                        localStorage.removeItem('password_reset_success');
                        
                        function handleResetSuccess() {
                            localStorage.removeItem('password_reset_success');
                            handleReturnToLogin();
                        }
                        
                        // Listen for storage event (triggered when password is changed in Tab C)
                        window.addEventListener('storage', function(e) {
                            if (e.key === 'password_reset_success' && e.newValue === 'true') {
                                handleResetSuccess();
                            }
                        });
                        
                        // Periodic polling check in case storage events are not supported/blocked
                        var checkTimer = setInterval(function() {
                            if (localStorage.getItem('password_reset_success') === 'true') {
                                clearInterval(checkTimer);
                                handleResetSuccess();
                            }
                        }, 1000);
                    })();
                </script>
            <#else>
                <#-- Success / Already Logged In Page: Trigger storage event and auto-redirect to login -->
                <div id="redirect-counter" style="font-size: 13px; color: #6b7280; margin-bottom: 20px;">
                    Redirecting to login in <span id="countdown-sec" style="font-weight: 600; color: #6D3E93;">5</span> seconds...
                </div>
                <a href="${url.loginUrl}" onclick="event.preventDefault(); handleReturnToLogin();" class="pf-c-button pf-m-primary" style="text-decoration: none; display: inline-block; padding: 10px 24px; background: #6D3E93; color: white; border-radius: 8px; font-weight: 600; font-size: 15px;">
                    Go to Login Page
                </a>
                <script>
                    (function() {
                        // Set the success flag to notify Tab A
                        localStorage.setItem('password_reset_success', 'true');
                        
                        var sec = 5;
                        var timer = setInterval(function() {
                            sec--;
                            var el = document.getElementById('countdown-sec');
                            if (el) el.innerText = sec;
                            if (sec <= 0) {
                                clearInterval(timer);
                                handleReturnToLogin();
                            }
                        }, 1000);
                    })();
                </script>
            </#if>
        </div>
    </#if>
</@layout.registrationLayout>
