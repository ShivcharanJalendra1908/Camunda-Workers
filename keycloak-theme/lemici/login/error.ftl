<#import "template.ftl" as layout>
<@layout.registrationLayout displayMessage=true; section>
    <#if section = "header">
        <div class="login-header">
            <h1>Email Verification</h1>
            <p>This link may have expired or already been used.</p>
        </div>
    <#elseif section = "form">
        <div id="kc-error-message" style="text-align: center; padding: 20px 0;">
            <div style="width: 64px; height: 64px; background: rgba(217, 119, 6, 0.1); color: #d97706; border-radius: 50%; display: flex; align-items: center; justify-content: center; margin: 0 auto 24px auto;">
                <svg fill="none" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" style="width: 32px; height: 32px;">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
                </svg>
            </div>

            <p style="font-size: 15px; color: #374151; margin-bottom: 8px; line-height: 1.6;">
                The verification link you clicked is no longer valid.
            </p>
            <p style="font-size: 14px; color: #6b7280; margin-bottom: 32px; line-height: 1.6;">
                This can happen if the link expired, was already used, or was opened on a different device or browser.
            </p>

            <div style="margin-bottom: 16px;">
                <a href="#" onclick="event.preventDefault(); handleReturnToLogin();" class="pf-c-button pf-m-primary" style="text-decoration: none; display: inline-block; padding: 12px 36px; background: #6D3E93; color: white; border-radius: 8px; font-weight: 600; font-size: 16px; box-shadow: 0 4px 10px rgba(109, 62, 147, 0.25);">
                    Sign In
                </a>
            </div>

            <p style="font-size: 13px; color: #9ca3af; line-height: 1.5; margin-top: 16px;">
                Sign in with your email and password — a new verification email will be sent if needed.
            </p>

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
            </script>
        </div>
    </#if>
</@layout.registrationLayout>
