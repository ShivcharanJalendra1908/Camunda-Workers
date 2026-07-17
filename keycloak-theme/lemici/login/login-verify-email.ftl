<#import "template.ftl" as layout>
<@layout.registrationLayout displayInfo=true; section>
    <#if section = "header">
        <div class="login-header">
            <h1>Email Verification</h1>
            <p>Verify your email to secure your account.</p>
        </div>
    <#elseif section = "form">
        <div class="text-center" style="text-align: center; padding: 20px 0;">
            <div style="width: 64px; height: 64px; background: rgba(109, 62, 147, 0.1); color: #6D3E93; border-radius: 50%; display: flex; align-items: center; justify-content: center; margin: 0 auto 24px auto;">
                <svg fill="none" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" style="width: 32px; height: 32px;">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M21.75 6.75v10.5a2.25 2.25 0 01-2.25 2.25h-15a2.25 2.25 0 01-2.25-2.25V6.75m19.5 0A2.25 2.25 0 0019.5 4.5h-15a2.25 2.25 0 00-2.25 2.25m19.5 0v.243a2.25 2.25 0 01-1.07 1.916l-7.5 4.615a2.25 2.25 0 01-2.36 0l-7.5-4.615a2.25 2.25 0 01-1.07-1.916V6.75" />
                </svg>
            </div>
            
            <p style="font-size: 16px; line-height: 1.6; color: #374151; margin-bottom: 24px;">
                An email with instructions to verify your email address has been sent to:<br>
                <span style="display: inline-block; background: rgba(109, 62, 147, 0.08); color: #6D3E93; padding: 6px 18px; border-radius: 20px; font-weight: 700; font-size: 15px; margin-top: 10px; border: 1px solid rgba(109, 62, 147, 0.15); word-break: break-all;">
                    ${user.email}
                </span>
            </p>
            
            <p style="font-size: 14px; color: #6b7280; margin-bottom: 24px; line-height: 1.5;">
                Once you click the link in the email to verify, please click "Sign In" below.
            </p>

            <div style="margin-bottom: 32px;">
                <a href="#" onclick="event.preventDefault(); window.location.href = getBffLoginStartUrl();" class="pf-c-button pf-m-primary" style="text-decoration: none; display: inline-block; padding: 12px 36px; background: #6D3E93; color: white; border-radius: 8px; font-weight: 600; font-size: 16px; box-shadow: 0 4px 10px rgba(109, 62, 147, 0.25);">
                    Sign In
                </a>
            </div>
            
            <div style="border-top: 1px solid #e5e7eb; padding-top: 24px; margin-top: 24px;">
                <p style="font-size: 14px; color: #4b5563; margin-bottom: 12px;">
                    Haven't received the email yet?
                </p>
                <a href="${url.loginAction}" class="pf-c-button pf-m-secondary" style="text-decoration: none; display: inline-block; padding: 10px 24px; border: 1px solid #6D3E93; color: #6D3E93; border-radius: 8px; font-weight: 600; font-size: 14px;">
                    Resend Verification Email
                </a>
            </div>
        </div>

        <script>
            function getBffLoginStartUrl() {
                var origin = window.location.origin;
                if (window.location.hostname === 'localhost' || window.location.hostname === '127.0.0.1') {
                    return 'http://localhost:8080/api/v1/auth/login/start';
                }
                return origin + '/api/v1/auth/login/start';
            }
        </script>
    </#if>
</@layout.registrationLayout>
